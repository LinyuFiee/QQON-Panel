// Package buildinfo carries the small public identity of a release.
package buildinfo

import "runtime"

const DataSchema = 1

type Info struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	BuildTime  string `json:"buildTime"`
	Repository string `json:"repository"`
	DataSchema int    `json:"dataSchema"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
}

func Current(version, commit, buildTime, repository string) Info {
	return Info{Version: version, Commit: commit, BuildTime: buildTime, Repository: repository, DataSchema: DataSchema, OS: runtime.GOOS, Arch: runtime.GOARCH}
}
