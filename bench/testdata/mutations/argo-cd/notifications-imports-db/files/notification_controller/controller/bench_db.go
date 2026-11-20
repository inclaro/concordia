package controller

import "github.com/argoproj/argo-cd/v3/util/db"

func benchCredentialStore() error {
	_, err := db.URIToSecretName("repo", "https://example.invalid/bench.git")
	return err
}
