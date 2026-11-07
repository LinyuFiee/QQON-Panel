package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"sort"

	"panel/internal/jobrunner"
)

type stateFile struct {
	Format int      `json:"format"`
	Sites  []record `json:"sites"`
}

type transaction struct {
	Format int     `json:"format"`
	ID     string  `json:"id"`
	Before *record `json:"before,omitempty"`
	After  *record `json:"after,omitempty"`
}

func stateBytes(records map[string]record) ([]byte, error) {
	state := stateFile{Format: 1, Sites: make([]record, 0, len(records))}
	for _, r := range records {
		state.Sites = append(state.Sites, r)
	}
	sort.Slice(state.Sites, func(i, j int) bool { return state.Sites[i].Site.ID < state.Sites[j].Site.ID })
	data, err := json.Marshal(state)
	if err != nil || len(data) > maxStateBytes {
		return nil, errors.New("sites: state exceeds limit")
	}
	return data, nil
}

func parseJSON(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return errors.New("sites: invalid bounded JSON")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("sites: trailing JSON")
	}
	return nil
}

func (s *Service) validRecord(r record) bool {
	if !idPattern.MatchString(r.Site.ID) || !idPattern.MatchString(r.Token) || !hashPattern.MatchString(r.ConfigHash) || r.Site.Revision == 0 || r.Site.CreatedAt.IsZero() || r.Site.UpdatedAt.Before(r.Site.CreatedAt) {
		return false
	}
	in, err := s.validateInput(inputFromSite(r.Site))
	if err != nil || !reflect.DeepEqual(in, inputFromSite(r.Site)) {
		return false
	}
	if r.Site.Type == "php" && !knownPHPSocket(r.Site.PHPRuntime, r.PHPSocket) || r.Site.Type != "php" && r.PHPSocket != "" {
		return false
	}
	if r.Site.TLSEnabled {
		if !idPattern.MatchString(r.CertGeneration) || !hashPattern.MatchString(r.CertHash) || r.Site.Port == 80 {
			return false
		}
	} else if r.CertGeneration != "" || r.CertHash != "" {
		return false
	}
	if r.ProxyCA != "" && (!knownCA(r.ProxyCA) || r.Site.Type != "proxy") {
		return false
	}
	derived := r
	s.setDerived(&derived)
	if !reflect.DeepEqual(r.Site, derived.Site) {
		return false
	}
	config, err := s.render(r)
	return err == nil && digest(config) == r.ConfigHash
}

func (s *Service) load() error {
	root, err := absoluteRoot(s.statePath, false, 0700, true, s.adapters.strict)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("sites: unsafe state directory")
	}
	defer root.Close()
	data, err := readFile(root, "state.json", maxStateBytes, s.adapters.strict)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("sites: cannot read safe bounded state")
	}
	var state stateFile
	if parseJSON(data, &state) != nil || state.Format != 1 || len(state.Sites) > maxSites {
		return errors.New("sites: malformed state")
	}
	for _, r := range state.Sites {
		if _, exists := s.records[r.Site.ID]; exists || !s.validRecord(r) {
			return errors.New("sites: inconsistent site state")
		}
		if err := s.conflict(r); err != nil {
			return errors.New("sites: conflicting site state")
		}
		s.records[r.Site.ID] = r
	}
	s.stateHash = digest(data)
	return nil
}

func copyRecords(source map[string]record) map[string]record {
	next := make(map[string]record, len(source)+1)
	for id, r := range source {
		next[id] = r
	}
	return next
}

func (s *Service) persist(next map[string]record) error {
	data, err := stateBytes(next)
	if err != nil {
		return err
	}
	root, err := absoluteRoot(s.statePath, true, 0700, true, s.adapters.strict)
	if err != nil {
		return err
	}
	defer root.Close()
	s.mu.RLock()
	expected := s.stateHash
	s.mu.RUnlock()
	if err := atomicFile(root, "state.json", data, 0600, expected, maxStateBytes, s.adapters.strict); err != nil {
		return err
	}
	s.mu.Lock()
	s.records, s.stateHash = next, digest(data)
	s.mu.Unlock()
	return nil
}

func (s *Service) verifyState() error {
	root, err := absoluteRoot(s.statePath, true, 0700, true, s.adapters.strict)
	if err != nil {
		return err
	}
	defer root.Close()
	hash, err := currentHash(root, "state.json", maxStateBytes, s.adapters.strict)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if hash != s.stateHash {
		return errConflict
	}
	return nil
}

func (s *Service) saveJournal(tx transaction) (string, error) {
	data, err := json.Marshal(tx)
	if err != nil || len(data) > 64<<10 {
		return "", errUnsafe
	}
	root, err := absoluteRoot(s.statePath, true, 0700, true, s.adapters.strict)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := atomicFile(root, "transaction.json", data, 0600, "", 64<<10, s.adapters.strict); err != nil {
		return "", err
	}
	return digest(data), nil
}

func (s *Service) clearJournal(expected string) error {
	root, err := absoluteRoot(s.statePath, false, 0700, true, s.adapters.strict)
	if err != nil {
		return err
	}
	defer root.Close()
	return removeOwned(root, "transaction.json", expected, 64<<10, s.adapters.strict)
}

func (s *Service) readJournal() (*transaction, string, error) {
	root, err := absoluteRoot(s.statePath, false, 0700, true, s.adapters.strict)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	data, err := readFile(root, "transaction.json", 64<<10, s.adapters.strict)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var tx transaction
	if parseJSON(data, &tx) != nil || tx.Format != 1 || !idPattern.MatchString(tx.ID) || tx.Before == nil && tx.After == nil {
		return nil, "", errUnsafe
	}
	for _, r := range []*record{tx.Before, tx.After} {
		if r != nil && (r.Site.ID != tx.ID || !s.validRecord(*r)) {
			return nil, "", errUnsafe
		}
	}
	if tx.Before != nil && tx.After != nil && (tx.After.Site.Revision != tx.Before.Site.Revision+1 || tx.After.Site.CreatedAt != tx.Before.Site.CreatedAt) {
		return nil, "", errUnsafe
	}
	if tx.Before == nil && tx.After.Site.Revision != 1 {
		return nil, "", errUnsafe
	}
	return &tx, digest(data), nil
}

func (s *Service) recover(ctx context.Context) error {
	tx, hash, err := s.readJournal()
	if err != nil || tx == nil {
		return err
	}
	if err := s.permission(); err != nil {
		return err
	}
	info, err := s.runtimeInfo(ctx)
	if err != nil {
		return err
	}
	return s.rollback(ctx, *tx, hash, info.NginxBinary, nil)
}

func (s *Service) rollback(ctx context.Context, tx transaction, journalHash, nginx string, p *jobrunner.Progress) error {
	if err := s.permission(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	updateProgress(p, "rollback", 90, "恢复上一版受管配置；网站文件始终保留")
	config, err := absoluteRoot(s.adapters.configRoot, false, 0755, false, s.adapters.strict)
	if err != nil {
		return err
	}
	defer config.Close()
	name := "qqon-" + tx.ID + ".conf"
	actual, err := currentHash(config, name, maxConfigBytes, s.adapters.strict)
	if err != nil {
		return err
	}
	beforeHash, afterHash := "", ""
	if tx.Before != nil {
		beforeHash = tx.Before.ConfigHash
	}
	if tx.After != nil {
		afterHash = tx.After.ConfigHash
	}
	if actual != beforeHash && actual != afterHash {
		return errConflict
	}
	if actual != beforeHash {
		if tx.Before == nil {
			if err := removeOwned(config, name, actual, maxConfigBytes, s.adapters.strict); err != nil {
				return err
			}
		} else {
			old, err := s.render(*tx.Before)
			if err != nil {
				return err
			}
			if err := atomicFile(config, name, old, 0644, actual, maxConfigBytes, s.adapters.strict); err != nil {
				return err
			}
		}
	}
	if err := s.runCommand(ctx, "test", nginx); err != nil {
		return err
	}
	if err := s.runCommand(ctx, "reload", nginx); err != nil {
		return err
	}
	if tx.Before != nil {
		if err := s.adapters.verify(ctx, *tx.Before, false); err != nil {
			return err
		}
	} else if tx.After != nil {
		if err := s.adapters.verifyRemoved(ctx, *tx.After); err != nil {
			return err
		}
	}
	if err := s.permission(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	next := copyRecords(s.records)
	current, exists := next[tx.ID]
	s.mu.RUnlock()
	matchesBefore := !exists && tx.Before == nil || exists && tx.Before != nil && reflect.DeepEqual(current, *tx.Before)
	matchesAfter := !exists && tx.After == nil || exists && tx.After != nil && reflect.DeepEqual(current, *tx.After)
	if !matchesBefore && !matchesAfter {
		return errConflict
	}
	if tx.Before == nil {
		delete(next, tx.ID)
	} else {
		next[tx.ID] = *tx.Before
	}
	if err := s.persist(next); err != nil {
		return err
	}
	return s.clearJournal(journalHash)
}
