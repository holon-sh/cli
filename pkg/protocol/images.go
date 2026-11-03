package protocol

import (
	"net/url"
	"slices"
	"strings"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
)

func ValidDigest(value string) bool {
	parsed, err := digest.Parse(value)
	return err == nil && parsed.Algorithm() == digest.SHA256
}

func ValidRepository(value string) bool {
	named, err := reference.WithName("localhost/" + value)
	return err == nil && reference.Path(named) == value
}

func ValidRegistryHost(value string) bool {
	if len(value) > 253 || strings.TrimSpace(value) != value {
		return false
	}
	parsed, err := url.Parse("//" + value)
	return err == nil && parsed.Host == value && parsed.Hostname() != ""
}

func ValidImageRepositoryPrefix(prefix string) bool {
	if prefix == "" {
		return true
	}
	host, repository, _ := strings.Cut(prefix, "/")
	named, err := reference.ParseNamed(prefix + "/holon-validation")
	return err == nil && reference.Domain(named) == host && reference.Path(named) == repository+"/holon-validation"
}

func IsValidDigestImage(image string) bool {
	named, _ := reference.ParseNamed(image)
	digested, ok := named.(reference.Digested)
	return ok && digested.Digest().Algorithm() == digest.SHA256
}

func ValidBuildArchitectures(architectures []string) bool {
	return (len(architectures) < 2 || len(architectures) == 2 && architectures[0] != architectures[1]) &&
		!slices.ContainsFunc(architectures, func(architecture string) bool {
			return !slices.Contains([]string{"amd64", "arm64"}, architecture)
		})
}
