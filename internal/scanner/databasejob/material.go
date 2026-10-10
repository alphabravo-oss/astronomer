package databasejob

import "context"

const ProjectedJobMaterialRoot = "/run/astronomer/projected"
const PrivateJobMaterialRoot = "/run/astronomer/authority"
const JobProcessUID = 65534

// CopyProjectedPreparationMaterial is an explicit root init-process adapter,
// not a relaxation of the canonical verifier's no-symlink regular-file reader.
// Kubernetes projected symlinks are opened only beneath a pinned known epoch;
// the later nonroot preparation process consumes private regular copies through
// SAME canonical trust parsers/verifier. Copying grants no authentication.
// The worker must revalidate ORIGINAL server mounts and current079 rows before
// every state transition/report commit. An immutable copied Secret cannot hide
// later original key/root/pull rotation. No material or material hash is logged.
func CopyProjectedPreparationMaterial(ctx context.Context, privatePull bool) error {
	return copyProjectedPreparationMaterial(ctx, ProjectedJobMaterialRoot, PrivateJobMaterialRoot, JobProcessUID, privatePull, nil)
}

// CopyProjectedMappedPreparationMaterial is the constructor-only mapped release
// variant. The bounded original release mapping is copied unchanged into the
// closed epoch; config.Load later applies the SAME canonical releasecontract
// binding. It is locator DATA, not signature or network permission. The legacy
// six/seven-member adapter remains unchanged and rejects an extra mapping.
func CopyProjectedMappedPreparationMaterial(ctx context.Context, privatePull bool) error {
	return copyProjectedPreparationMaterialWithMapping(ctx, ProjectedJobMaterialRoot, PrivateJobMaterialRoot, JobProcessUID, privatePull, true, nil)
}
