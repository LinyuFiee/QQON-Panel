package machine

import (
	"context"
	"testing"
)

func TestServiceSnapshotDiscoversActualPHPVersions(t *testing.T) {
	view := queryServices(context.Background(), "fixture", func(context.Context, string, []string) ([]byte, error) {
		return []byte("Id=nginx.service\nActiveState=active\nLoadState=loaded\n\n" +
			"Id=php8.2-fpm.service\nActiveState=active\nLoadState=loaded\n\n" +
			"Id=php8.4-fpm.service\nActiveState=inactive\nLoadState=loaded\n\n" +
			"Id=php-fpm.service\nActiveState=inactive\nLoadState=not-found\n\n" +
			"Id=php-evil-fpm.service\nActiveState=active\nLoadState=loaded\n"), nil
	})
	if !view.Available || len(view.Services) != len(serviceAllowlist)+2 {
		t.Fatalf("unexpected PHP discovery: %+v", view)
	}
	states := map[string]string{}
	for _, service := range view.Services {
		states[service.ID] = service.State
	}
	if states["php8.2-fpm"] != "active" || states["php8.4-fpm"] != "inactive" {
		t.Fatal("actual PHP units missing")
	}
	for _, absent := range []string{"php8.3-fpm", "php-fpm", "php-evil-fpm"} {
		if _, ok := states[absent]; ok {
			t.Fatalf("invented PHP unit %s", absent)
		}
	}
}
