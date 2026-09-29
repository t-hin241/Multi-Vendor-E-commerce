package config

import "fmt"

type InternalServices struct{ Key, IdentityURL string }

func LoadInternalServices() (InternalServices, error) {
	key, err := requireEnv("IDENTITY_SERVICE_KEY")
	if err != nil {
		return InternalServices{}, err
	}
	if len(key) < 32 || key == "CHANGE_ME" {
		return InternalServices{}, fmt.Errorf("IDENTITY_SERVICE_KEY must contain at least 32 characters")
	}
	return InternalServices{Key: key, IdentityURL: getEnv("IDENTITY_SERVICE_URL", "http://identity:8081")}, nil
}
