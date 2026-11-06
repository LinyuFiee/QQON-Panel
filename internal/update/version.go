package update

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?/[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,98}[A-Za-z0-9])?$`)

type Version struct {
	Major int64
	Minor int64
	Patch int64
}

func ParseVersion(value string) (Version, error) {
	if len(value) > 32 || !stableVersion.MatchString(value) {
		return Version{}, errors.New("版本必须为不含前导零的 MAJOR.MINOR.PATCH 稳定版本")
	}
	parts := strings.Split(value, ".")
	var numbers [3]int64
	for index, part := range parts {
		number, err := strconv.ParseInt(part, 10, 32)
		if err != nil {
			return Version{}, errors.New("版本号超出允许范围")
		}
		numbers[index] = number
	}
	return Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, nil
}

func (v Version) Compare(other Version) int {
	left, right := [3]int64{v.Major, v.Minor, v.Patch}, [3]int64{other.Major, other.Minor, other.Patch}
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}

func ValidateRepository(repository string) error {
	if len(repository) > 201 || !repositoryPattern.MatchString(repository) {
		return errors.New("GitHub 仓库必须为合法的 owner/repo，不接受 URL、凭据或路径参数")
	}
	return nil
}
