package provider

import (
 "context"
 "errors"

 "github.com/google/uuid"

 "github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// The ordinary projection may deliberately leave stale historical state
// unrenewed while delivering valid new desired intent. A scan cannot mistake
// that no-op for permission: independently compare this locked state to the
// SAME canonical builtin/model/rollout compiler's actual current DATA.
func (owner *DatabaseScanOwner) lockCurrentDatabaseScanState(ctx context.Context,queries *sqlc.Queries,cluster uuid.UUID)(sqlc.VulnerabilityDatabaseClusterState,error) {
 empty:=sqlc.VulnerabilityDatabaseClusterState{}
 if owner==nil||owner.projection==nil||queries==nil{return empty,errors.New("owned database current state owner unavailable")}
 catalog,err:=owner.projection.authority.Catalog(ctx,owner.projection.proof)
 if err!=nil{return empty,err}
 if err:=queries.LockVulnerabilityDatabaseAuthority(ctx);err!=nil{return empty,err}
 base,err:=queries.GetCurrentVulnerabilityDatabaseBaseForUpdate(ctx)
 if err!=nil||!databaseProjectionBaseMatches(base,owner.projection.snapshot,owner.projection.bindingFingerprint) {return empty,errors.New("owned database current durable base differs")}
 state,err:=queries.GetVulnerabilityDatabaseClusterStateForUpdate(ctx,cluster)
 if err!=nil{return empty,err}
 intent,err:=lockDatabaseScannerIntent(ctx,queries,cluster,catalog)
 if err!=nil{return empty,err}
 expected,effective,deployment:=intent.expected,intent.effective,intent.deployment
 if state.BaseID!=base.ID||state.BaseEpoch!=base.Epoch||state.ProjectID!=expected.ProjectID||state.SourceID!=expected.SourceID||state.VersionID!=expected.VersionID||state.TargetID!=expected.TargetID||state.TargetGeneration!=intent.generation||state.BundleSpecDigest!=expected.Version.SpecDigest||state.DeploymentID!=deployment.ID||state.DeploymentGeneration!=deployment.DesiredGeneration||state.ConfigurationDigest!=effective.ConfigurationDigest.String()||state.DesiredSpecDigest!=effective.SpecDigest.String() {return empty,errors.New("owned database current scanner intent differs")}
 if _,err:=queries.LockVulnerabilityDatabaseCurrentUpdate(ctx,sqlc.LockVulnerabilityDatabaseCurrentUpdateParams{UpdateID:state.UpdateID,BaseID:base.ID,BaseEpoch:base.Epoch,UpdateGeneration:state.UpdateGeneration});err!=nil{return empty,err}
 return state,nil
}
