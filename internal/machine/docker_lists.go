package machine

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type containerView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Ports   string `json:"ports"`
	Created string `json:"created"`
}

type imageView struct {
	ID      string   `json:"id"`
	Tags    []string `json:"tags"`
	Size    int64    `json:"size"`
	Created string   `json:"created"`
}

type volumeView struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint"`
}

type networkView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope"`
}

func dockerInvalidList() error {
	return problem(http.StatusBadGateway, "docker_list_limit", "Docker 列表无效或超过安全条数限制")
}

func dockerTimestamp(seconds int64) (string, error) {
	if seconds < -62135596800 || seconds > 253402300799 {
		return "", dockerInvalidList()
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339), nil
}

func (s *Service) handleContainers(w http.ResponseWriter, r *http.Request) {
	s.dockerRead(w, r, func(ctx context.Context) (any, error) {
		var raw []struct {
			ID      string `json:"Id"`
			Names   []string
			Image   string
			State   string
			Status  string
			Created int64
			Ports   []struct {
				IP          string
				PrivatePort uint16
				PublicPort  uint16
				Type        string
			}
		}
		if err := s.docker.getJSON(ctx, "/containers/json", url.Values{"all": {"1"}}, &raw); err != nil {
			return nil, err
		}
		if raw == nil || len(raw) > dockerListLimit {
			return nil, dockerInvalidList()
		}
		items := make([]containerView, 0, len(raw))
		for _, item := range raw {
			if !validContainerID(item.ID) || len(item.Ports) > 256 || len(item.Names) > 256 {
				return nil, dockerInvalidList()
			}
			created, err := dockerTimestamp(item.Created)
			if err != nil {
				return nil, err
			}
			name := item.ID[:12]
			if len(item.Names) > 0 {
				name = safeDisplay(strings.TrimPrefix(item.Names[0], "/"), 256)
			}
			ports := make([]string, 0, len(item.Ports))
			for _, port := range item.Ports {
				entry := fmt.Sprintf("%d/%s", port.PrivatePort, safeDisplay(port.Type, 16))
				if port.PublicPort != 0 {
					entry = fmt.Sprintf("%s:%d→%s", safeDisplay(port.IP, 64), port.PublicPort, entry)
				}
				ports = append(ports, entry)
			}
			items = append(items, containerView{item.ID, name, safeDisplay(item.Image, 512), safeDisplay(item.State, 64), safeDisplay(item.Status, 512), safeDisplay(strings.Join(ports, ", "), 2048), created})
		}
		return struct {
			Containers []containerView `json:"containers"`
		}{items}, nil
	})
}

func (s *Service) handleImages(w http.ResponseWriter, r *http.Request) {
	s.dockerRead(w, r, func(ctx context.Context) (any, error) {
		var raw []struct {
			ID       string `json:"Id"`
			RepoTags []string
			Size     int64
			Created  int64
		}
		if err := s.docker.getJSON(ctx, "/images/json", nil, &raw); err != nil {
			return nil, err
		}
		if raw == nil || len(raw) > dockerListLimit {
			return nil, dockerInvalidList()
		}
		items := make([]imageView, 0, len(raw))
		for _, item := range raw {
			if len(item.RepoTags) > 128 || item.Size < 0 || len(item.ID) > 128 {
				return nil, dockerInvalidList()
			}
			created, err := dockerTimestamp(item.Created)
			if err != nil {
				return nil, err
			}
			tags := make([]string, 0, len(item.RepoTags))
			for _, tag := range item.RepoTags {
				tags = append(tags, safeDisplay(tag, 512))
			}
			items = append(items, imageView{safeDisplay(item.ID, 128), tags, item.Size, created})
		}
		return struct {
			Images []imageView `json:"images"`
		}{items}, nil
	})
}

func (s *Service) handleVolumes(w http.ResponseWriter, r *http.Request) {
	s.dockerRead(w, r, func(ctx context.Context) (any, error) {
		var raw *struct {
			Volumes []struct{ Name, Driver, Mountpoint string }
		}
		if err := s.docker.getJSON(ctx, "/volumes", nil, &raw); err != nil {
			return nil, err
		}
		if raw == nil || len(raw.Volumes) > dockerListLimit {
			return nil, dockerInvalidList()
		}
		items := make([]volumeView, 0, len(raw.Volumes))
		for _, item := range raw.Volumes {
			items = append(items, volumeView{safeDisplay(item.Name, 256), safeDisplay(item.Driver, 128), safeDisplay(item.Mountpoint, 1024)})
		}
		return struct {
			Volumes []volumeView `json:"volumes"`
		}{items}, nil
	})
}

func (s *Service) handleNetworks(w http.ResponseWriter, r *http.Request) {
	s.dockerRead(w, r, func(ctx context.Context) (any, error) {
		var raw []struct{ ID, Name, Driver, Scope string }
		if err := s.docker.getJSON(ctx, "/networks", nil, &raw); err != nil {
			return nil, err
		}
		if raw == nil || len(raw) > dockerListLimit {
			return nil, dockerInvalidList()
		}
		items := make([]networkView, 0, len(raw))
		for _, item := range raw {
			items = append(items, networkView{safeDisplay(item.ID, 128), safeDisplay(item.Name, 256), safeDisplay(item.Driver, 128), safeDisplay(item.Scope, 64)})
		}
		return struct {
			Networks []networkView `json:"networks"`
		}{items}, nil
	})
}
