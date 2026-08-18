package storage

import "path/filepath"

// BlobsDir is the encrypted CAS root for a tenant.
func BlobsDir(tenantPath string) string {
	return filepath.Join(tenantPath, "blobs")
}

// ManifestsDir holds encrypted generation manifests.
func ManifestsDir(tenantPath string) string {
	return filepath.Join(tenantPath, "manifests")
}
