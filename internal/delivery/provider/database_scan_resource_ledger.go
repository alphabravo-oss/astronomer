package provider

import (
 "context"
 "errors"
 "net/url"

 "github.com/google/uuid"
 "k8s.io/apimachinery/pkg/types"

 "github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
 "github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
)

const databaseScanMaximumUnremovedAllocations int64 = 32
var errDatabaseScanResourceCapacity = errors.New("owned database scan resource capacity exhausted; terminal ownership repair required")

// A partial create with no durably checked UID remains counted even when a
// later GET returns 404: that observation cannot join an in-flight POST. This
// status never grants deletion or adoption by name. Repair must establish exact
// UID ownership through the canonical active transition or operator inspection.
func databaseScanUnknownProgress(err error,staticMessage string)error {
 if errors.Is(err,errDatabaseScanResourceCapacity){return errDatabaseScanResourceCapacity}
 return errors.New(staticMessage)
}
func databaseScanAllocationAvailable(unremoved int64,currentAllocated bool)bool {
 return unremoved>=0&&unremoved<=databaseScanMaximumUnremovedAllocations&&(currentAllocated||unremoved<databaseScanMaximumUnremovedAllocations)
}

// Exact constructor-produced names only; shared Namespace is never deleted.
func databaseScanResourceRole(claim uuid.UUID,path string)(string,bool) {
 if claim==uuid.Nil{return "",false}
 prefix:="database-scan-"+claim.String()
 for role,suffix:=range map[string]string{"preparation":"/apis/batch/v1/namespaces/"+databasejob.ScanNamespace+"/jobs/"+prefix+"-prepare","scan":"/apis/batch/v1/namespaces/"+databasejob.ScanNamespace+"/jobs/"+prefix+"-scan","authority":"/api/v1/namespaces/"+databasejob.ScanNamespace+"/secrets/"+prefix+"-authority","image-pull":"/api/v1/namespaces/"+databasejob.ScanNamespace+"/secrets/"+prefix+"-image-pull","input":"/api/v1/namespaces/"+databasejob.ScanNamespace+"/persistentvolumeclaims/"+prefix+"-input","deny":"/apis/networking.k8s.io/v1/namespaces/"+databasejob.ScanNamespace+"/networkpolicies/"+prefix+"-deny"} {
  if path==suffix{return role,true}
 }
 return "",false
}
func (owner *DatabaseScanOwner) registerScanResourceIntent(ctx context.Context,claim sqlc.VulnerabilityDatabaseScanClaim,path,hash string)error {
 if path=="/api/v1/namespaces/"+databasejob.ScanNamespace{return nil}
 role,valid:=databaseScanResourceRole(claim.ID,path)
 if !valid||!validDatabaseScanDigest(hash){return errors.New("owned database resource intent outside claim")}
 exhausted:=false
 err:=owner.transitionClaim(ctx,claim,func(ctx context.Context,queries *sqlc.Queries,current sqlc.VulnerabilityDatabaseScanClaim)error {
  // The canonical participant already holds the cluster session/current rows
  // before this read. SQL independently enforces the same finite cap at INSERT.
  count,err:=queries.GetOwnedDatabaseResourceAllocationCount(ctx,sqlc.GetOwnedDatabaseResourceAllocationCountParams{ClusterID:current.ClusterID,ClaimID:current.ID})
  if err!=nil{return err}
  if !databaseScanAllocationAvailable(count.Unremoved,count.CurrentAllocated) {exhausted=true;return errDatabaseScanResourceCapacity}
  _,err=queries.RegisterOwnedDatabaseResourceIntent(ctx,sqlc.RegisterOwnedDatabaseResourceIntentParams{ClaimID:current.ID,Role:role,ResourcePath:path,RequestDigest:hash});return err
 })
 if ctx.Err()!=nil{return ctx.Err()}
 // The transaction sanitizer deliberately strips arbitrary child errors.
 // Re-expose ONLY this constructor-owned, actually locked capacity observation
 // as a static UNKNOWN status; no request-selected diagnostic survives.
 if err!=nil&&exhausted{return errDatabaseScanResourceCapacity}
 return err
}
// Called ONLY after each typed object reader checked current kind, canonical
// content/spec, claim labels and immutable UID. A partial creation cannot open
// another allocation indefinitely: its pre-POST intent remains counted.
func (owner *DatabaseScanOwner) recordScanResourceUID(ctx context.Context,claim sqlc.VulnerabilityDatabaseScanClaim,path string,uid types.UID,readOnly bool)error {
 if readOnly{return nil}
 if _,valid:=databaseScanResourceRole(claim.ID,path);!valid||!validDatabaseScanUID(uid){return errors.New("owned database resource UID unavailable")}
 return owner.transitionClaim(ctx,claim,func(ctx context.Context,queries *sqlc.Queries,current sqlc.VulnerabilityDatabaseScanClaim)error {
  return requireDatabaseScanWrite(queries.RecordOwnedDatabaseResourceUID(ctx,sqlc.RecordOwnedDatabaseResourceUIDParams{ClaimID:current.ID,ResourcePath:path,ResourceUid:uuid.MustParse(string(uid))}))
 })
}
func databaseScanResourceName(path string)string {
 parsed,err:=url.ParseRequestURI(path);if err!=nil{return ""}
 for index:=len(parsed.Path)-1;index>=0;index-- {if parsed.Path[index]=='/' {return parsed.Path[index+1:]}}
 return ""
}
