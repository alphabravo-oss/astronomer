package provider

import (
 "context"
 "errors"
 "math"
 "strings"
 "time"

 "github.com/google/uuid"
 "github.com/jackc/pgx/v5"

 "github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
 "github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
 "github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
 "github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Reservation is private owner-selected DATA from actual current Pod/Node
// observations. Only the canonical provider transaction may persist its claim.
type databaseScanReservation struct {
 owner *DatabaseScanOwner
 workload sqlc.VulnerabilityDatabaseScanClaim
 metadata resolver.WorkloadImageMetadata
 update *distribution.AuthenticatedDatabaseUpdate
 claimID uuid.UUID
}
func (reservation *databaseScanReservation) revalidateOwnedSnapshot(ctx context.Context)error {
 if reservation==nil||reservation.owner==nil||reservation.update==nil {return errors.New("owned database reservation unavailable")}
 if err:=reservation.owner.revalidateScanMaterial(ctx);err!=nil{return err}
 if err:=reservation.owner.projection.revalidateOwnedSnapshot(ctx);err!=nil{return err}
 return reservation.update.ValidateCurrentMetadata(time.Now().UTC())
}
func (reservation *databaseScanReservation) prepareOwnedSnapshot(ctx context.Context,queries *sqlc.Queries,cluster uuid.UUID)(ownedSnapshotRefresh,error) {
 if reservation.workload.ClusterID!=cluster {return nil,errors.New("owned database reservation cluster differs")}
 refresh,err:=reservation.owner.projection.prepareOwnedSnapshot(ctx,queries,cluster)
 if err!=nil{return nil,err}
 state,err:=reservation.owner.lockCurrentDatabaseScanState(ctx,queries,cluster)
 if err!=nil{return nil,err}
 digest,document,valid:=reservation.update.Snapshot()
 if !valid||digest!=reservation.workload.FeedDigest||document.Generation!=state.UpdateGeneration {return nil,errors.New("owned database reservation current signed generation differs")}
 update,err:=queries.LockVulnerabilityDatabaseCurrentUpdate(ctx,sqlc.LockVulnerabilityDatabaseCurrentUpdateParams{UpdateID:state.UpdateID,BaseID:state.BaseID,BaseEpoch:state.BaseEpoch,UpdateGeneration:state.UpdateGeneration})
 if err!=nil||update!=state.UpdateID {return nil,errors.New("owned database reservation current update differs")}
 retained,err:=queries.GetVulnerabilityDatabaseUpdateByFeedForUpdate(ctx,digest)
 if err!=nil||retained.ID!=state.UpdateID||retained.BaseID!=state.BaseID||retained.BaseEpoch!=state.BaseEpoch||retained.Generation!=state.UpdateGeneration {return nil,errors.New("owned database reservation retained feed differs")}
 intent,err:=queries.GetOwnedDatabaseRescanIntentForUpdate(ctx,sqlc.GetOwnedDatabaseRescanIntentForUpdateParams{ClusterID:cluster,UpdateID:state.UpdateID,TargetID:state.TargetID,TargetGeneration:state.TargetGeneration})
 if err!=nil{return nil,err}
 previous,err:=queries.GetOwnedVulnerabilityDatabaseScanCycleForUpdate(ctx,sqlc.GetOwnedVulnerabilityDatabaseScanCycleForUpdateParams{IntentID:intent.ID,WorkloadPodUid:reservation.workload.WorkloadPodUid,WorkloadContainerName:reservation.workload.WorkloadContainerName,WorkloadManifestDigest:reservation.workload.WorkloadManifestDigest})
 cycle:=int64(1)
 if err==nil {
  if previous.Phase=="pending-preparation"||previous.Phase=="preparing"||previous.Phase=="prepared"||previous.Phase=="scanning" {reservation.claimID=previous.ID;return refresh,nil}
  // A new accepted feed has a different intent. Under an unchanged feed, the
  // same six-hour automatic report cadence remains explicit and monotonic.
  if time.Now().UTC().Before(previous.UpdatedAt.Add(6*time.Hour)) {reservation.claimID=previous.ID;return refresh,nil}
  if previous.Cycle==math.MaxInt64 {return nil,errors.New("owned database reservation cycle exhausted")}
  cycle=previous.Cycle+1
 } else if !errors.Is(err,pgx.ErrNoRows) {return nil,err}
 return func(ctx context.Context,queries *sqlc.Queries,receipt sqlc.DeliveryAssignmentReceipt,response protocol.DeliveryStateResponseV2)error {
  if err:=refresh(ctx,queries,receipt,response);err!=nil{return err}
  if err:=reservation.revalidateOwnedSnapshot(ctx);err!=nil{return err}
  if err:=reservation.owner.recheckCurrentWorkload(ctx,reservation.workload,reservation.metadata);err!=nil{return err}
  value:=reservation.workload
  result,err:=queries.CreateOwnedVulnerabilityDatabaseScanClaim(ctx,sqlc.CreateOwnedVulnerabilityDatabaseScanClaimParams{ID:uuid.New(),IntentID:intent.ID,ClusterID:cluster,Cycle:cycle,
   AssignmentGeneration:receipt.DesiredSnapshotGeneration,AssignmentDigest:receipt.DesiredContentDigest,AssignmentEtag:receipt.DesiredSnapshotEtag,AssignmentCredentialEpoch:receipt.CredentialEpoch,AssignmentCredentialDigest:receipt.CredentialContentDigest,
   WorkloadNamespace:value.WorkloadNamespace,WorkloadPodName:value.WorkloadPodName,WorkloadPodUid:value.WorkloadPodUid,WorkloadContainerName:value.WorkloadContainerName,WorkloadRepository:value.WorkloadRepository,WorkloadManifestDigest:value.WorkloadManifestDigest,WorkloadImage:value.WorkloadImage,WorkloadSpecDigest:value.WorkloadSpecDigest,WorkloadRestartCount:value.WorkloadRestartCount,WorkloadContainerID:value.WorkloadContainerID,WorkloadImageID:value.WorkloadImageID,
   NodeUid:value.NodeUid,NodeName:value.NodeName,Architecture:value.Architecture,HelperIndexDigest:value.HelperIndexDigest,HelperChildDigest:value.HelperChildDigest,ScannerIndexDigest:value.ScannerIndexDigest,ScannerChildDigest:value.ScannerChildDigest})
  if err!=nil{return err}
  reservation.claimID=result.ID
  return reservation.revalidateOwnedSnapshot(ctx)
 },nil
}

func (owner *DatabaseScanOwner) reserveWorkload(ctx context.Context,workload sqlc.VulnerabilityDatabaseScanClaim,metadata resolver.WorkloadImageMetadata)(uuid.UUID,error) {
 if err:=owner.revalidateScanMaterial(ctx);err!=nil{return uuid.Nil,err}
 update,err:=owner.projection.authority.LoadUpdate(ctx,owner.projection.proof,owner.repository,workload.FeedDigest,time.Now().UTC())
 if err!=nil{return uuid.Nil,err}
 images,err:=owner.projection.authority.DatabaseScanImages(ctx,owner.projection.proof)
 if err!=nil{return uuid.Nil,err}
 for index,image:=range []distribution.DatabaseScanImage{images.Helper,images.Scanner} {
  _,digest,found:=strings.Cut(image.Reference,"@")
  if !found{return uuid.Nil,errors.New("owned database authenticated image reference invalid")}
  child:="";for _,platform:=range image.Platforms {if platform.Architecture==workload.Architecture {child=platform.Digest}}
  if child=="" {return uuid.Nil,errors.New("owned database selected architecture unavailable")}
  if index==0 {workload.HelperIndexDigest=digest;workload.HelperChildDigest=child}else{workload.ScannerIndexDigest=digest;workload.ScannerChildDigest=child}
 }
 participant:=&databaseScanReservation{owner:owner,workload:workload,metadata:metadata,update:update}
 provider,err:=newOwnedSnapshotCoordinator(owner.pool,owner.prototype,participant)
 if err!=nil{return uuid.Nil,err}
 bounded,cancel:=context.WithTimeout(ctx,30*time.Second);defer cancel()
 if _,err:=provider.ownedSnapshots.snapshot(bounded,workload.ClusterID,protocol.DeliveryStateRequestV2{});err!=nil{return uuid.Nil,err}
 return participant.claimID,nil
}
