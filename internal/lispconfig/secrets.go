package lispconfig

import (
	"fmt"
	"os"
)

// ResolveSecret resolves a declared name only from the inherited process
// environment. The operator chooses whether a shell profile, service manager,
// or secret manager populates that environment.
func ResolveSecret(name string) (string, error) {
	value, found := os.LookupEnv(name)
	if !found {
		return "", fmt.Errorf("required secret %s is absent from the process environment", name)
	}
	if value == "" {
		return "", fmt.Errorf("required secret %s is empty", name)
	}
	return value, nil
}
