package sites

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func preparedUpdate(t *testing.T, s *Service, old record) record {
	t.Helper()
	next := old
	next.Site = publicSite(old)
	next.Site.Enabled = false
	next.Site.Revision++
	next.Site.UpdatedAt = time.Now().UTC()
	if err := s.prepareRecord(context.Background(), &next, testRuntime()); err != nil {
		t.Fatal(err)
	}
	return next
}

func installJournal(t *testing.T, s *Service, before, after *record, changeConfig, commitMetadata bool) string {
	t.Helper()
	id := ""
	if before != nil {
		id = before.Site.ID
	}
	if after != nil {
		id = after.Site.ID
	}
	hash, err := s.saveJournal(transaction{Format: 1, ID: id, Before: before, After: after})
	if err != nil {
		t.Fatal(err)
	}
	if changeConfig {
		root, err := absoluteRoot(s.adapters.configRoot, false, 0755, false, false)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		expected := ""
		if before != nil {
			expected = before.ConfigHash
		}
		if after == nil {
			err = removeOwned(root, "qqon-"+id+".conf", expected, maxConfigBytes, false)
		} else {
			data, _ := s.render(*after)
			err = atomicFile(root, "qqon-"+id+".conf", data, 0644, expected, maxConfigBytes, false)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if commitMetadata {
		next := copyRecords(s.records)
		if after == nil {
			delete(next, id)
		} else {
			next[id] = *after
		}
		if err := s.persist(next); err != nil {
			t.Fatal(err)
		}
	}
	return hash
}

func TestInterruptedTransactionRecovery(t *testing.T) {
	for _, stage := range []string{"prepared", "config_replaced", "metadata_committed", "delete_removed"} {
		t.Run(stage, func(t *testing.T) {
			s, _, _ := fixture(t)
			old := createFixtureSite(t, s)
			next := preparedUpdate(t, s, old)
			after := &next
			if stage == "delete_removed" {
				after = nil
			}
			installJournal(t, s, &old, after, stage != "prepared", stage == "metadata_committed" || stage == "delete_removed")
			recovered, err := newService(s.options, s.adapters)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			if err := recovered.ready(); err != nil {
				t.Fatal(err)
			}
			got, err := recovered.snapshot(old.Site.ID)
			if err != nil || !reflect.DeepEqual(got, old) {
				t.Fatalf("recovery metadata mismatch: %v", err)
			}
			config, _ := os.ReadFile(filepath.Join(s.adapters.configRoot, "qqon-"+old.Site.ID+".conf"))
			if digest(config) != old.ConfigHash {
				t.Fatal("recovery config mismatch")
			}
			if _, err := os.Stat(filepath.Join(s.statePath, "transaction.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("recovery journal not cleared", err)
			}
			if _, err := os.Stat(filepath.Join(old.Site.Root, "index.html")); err != nil {
				t.Fatal("recovery deleted files", err)
			}
		})
	}
}

func TestInterruptedCreateRemovesConfigKeepsWebroot(t *testing.T) {
	s, _, _ := fixture(t)
	id, _ := randomID()
	in := staticInput("new.example.test")
	now := time.Now().UTC()
	next := record{Site: Site{ID: id, Name: in.Name, Domains: in.Domains, Type: in.Type, Port: in.Port, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := s.prepareRecord(context.Background(), &next, testRuntime()); err != nil {
		t.Fatal(err)
	}
	if err := s.createWebroot(next); err != nil {
		t.Fatal(err)
	}
	installJournal(t, s, nil, &next, true, true)
	recovered, err := newService(s.options, s.adapters)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err := recovered.ready(); err != nil {
		t.Fatal(err)
	}
	if len(recovered.siteList()) != 0 {
		t.Fatal("replayed interrupted create")
	}
	if _, err := os.Stat(filepath.Join(s.adapters.configRoot, "qqon-"+id+".conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created config retained", err)
	}
	if _, err := os.Stat(filepath.Join(next.Site.Root, "index.html")); err != nil {
		t.Fatal("removed site files", err)
	}
}

func TestRecoveryRefusesTamperedConfiguration(t *testing.T) {
	s, _, _ := fixture(t)
	old := createFixtureSite(t, s)
	next := preparedUpdate(t, s, old)
	installJournal(t, s, &old, &next, true, false)
	path := filepath.Join(s.adapters.configRoot, "qqon-"+old.Site.ID+".conf")
	unknown := []byte("# unknown administrator config\n")
	if err := os.WriteFile(path, unknown, 0644); err != nil {
		t.Fatal(err)
	}
	recovered, err := newService(s.options, s.adapters)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err := recovered.ready(); err == nil {
		t.Fatal("accepted writes after failed recovery")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, unknown) {
		t.Fatal("overwrote tampered config during recovery")
	}
	if _, err := os.Stat(filepath.Join(s.statePath, "transaction.json")); err != nil {
		t.Fatal("removed required recovery journal", err)
	}
}

func TestRollbackFailureFreezesAndRestartRecovers(t *testing.T) {
	s, h, _ := fixture(t)
	old := createFixtureSite(t, s)
	h.mu.Lock()
	h.fail = "reload"
	h.remaining = -1
	h.mu.Unlock()
	in := inputFromSite(old.Site)
	in.Enabled = false
	if err := s.patch(context.Background(), old.Site.ID, 1, in, nil); err == nil {
		t.Fatal("accepted repeated reload failure")
	}
	if err := s.ready(); err == nil {
		t.Fatal("writes not frozen")
	}
	if _, err := os.Stat(filepath.Join(s.statePath, "transaction.json")); err != nil {
		t.Fatal("journal lost")
	}
	h.mu.Lock()
	h.remaining = 0
	h.mu.Unlock()
	recovered, err := newService(s.options, s.adapters)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err := recovered.ready(); err != nil {
		t.Fatal(err)
	}
	got, _ := recovered.snapshot(old.Site.ID)
	if !reflect.DeepEqual(got, old) {
		t.Fatal("not restored after dependency recovered")
	}
}

func TestCancellationUsesIndependentRollbackContext(t *testing.T) {
	s, _, _ := fixture(t)
	old := createFixtureSite(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	s.adapters.command = func(c context.Context, action, binary string) error {
		calls++
		if calls == 1 {
			cancel()
			return context.Canceled
		}
		if err := c.Err(); err != nil {
			t.Errorf("rollback inherited cancellation: %v", err)
			return err
		}
		return nil
	}
	in := inputFromSite(old.Site)
	in.Enabled = false
	if err := s.patch(ctx, old.Site.ID, 1, in, nil); err == nil {
		t.Fatal("cancelled job succeeded")
	}
	if calls < 3 {
		t.Fatalf("rollback commands missing: %d", calls)
	}
	if err := s.ready(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.snapshot(old.Site.ID)
	if !reflect.DeepEqual(got, old) {
		t.Fatal("cancellation not rolled back")
	}
}

func TestStateTamperAndOversizeFailClosed(t *testing.T) {
	for _, kind := range []string{"unknown_fields", "oversize", "corrupt_hash"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := fixture(t)
			createFixtureSite(t, s)
			name := filepath.Join(s.statePath, "state.json")
			data, _ := os.ReadFile(name)
			switch kind {
			case "unknown_fields":
				data = []byte(`{"format":1,"sites":[],"command":"sh"}`)
			case "oversize":
				data = bytes.Repeat([]byte(" "), maxStateBytes+1)
			case "corrupt_hash":
				data = bytes.Replace(data, []byte(`"configHash":"`), []byte(`"configHash":"bad`), 1)
			}
			if err := os.WriteFile(name, data, 0600); err != nil {
				t.Fatal(err)
			}
			if restarted, err := newService(s.options, s.adapters); err == nil {
				restarted.Close()
				t.Fatal("accepted tampered state")
			}
		})
	}
}
