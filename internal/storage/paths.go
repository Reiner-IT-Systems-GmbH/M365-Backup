package storage

import "path/filepath"

// RepoDataDir is the on-disk Kopia filesystem repository for a tenant.
// Sibling dirs (sync/, exports/) stay outside so live sync is not mixed into blob storage.
func RepoDataDir(tenantPath string) string {
	return filepath.Join(tenantPath, "repo")
}

// RepoConfigFile is the local Kopia connection config (not required for CLI recovery).
func RepoConfigFile(tenantPath string) string {
	return filepath.Join(tenantPath, "kopia.config")
}

// RepoCacheDir holds Kopia content/metadata caches for this tenant.
func RepoCacheDir(tenantPath string) string {
	return filepath.Join(tenantPath, ".kopia-cache")
}
