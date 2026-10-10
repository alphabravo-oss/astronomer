package provider

import (
 "context"
 "errors"
 "time"

 "github.com/google/uuid"

 "github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Cursors are private, leader-local retrieval state, never persisted approval or
// coverage. Each finite phase resumes independently; an incomplete page rotates
// into the next sweep without making an incomplete fleet appear clean.
type databaseScanCursors struct {claims,clusters,cleanup uuid.UUID}
type databaseScanClaimEntry struct {id uuid.UUID}
type databaseScanSweepReader interface {
 claims(context.Context,uuid.UUID)([]databaseScanClaimEntry,error)
 clusters(context.Context,uuid.UUID)([]uuid.UUID,error)
}
type databaseScanSweepWork interface {
 dispatchClaim(context.Context,uuid.UUID)error
 discoverCluster(context.Context,uuid.UUID)error
}
type databaseScanSQLPages struct {queries *sqlc.Queries}
func (pages databaseScanSQLPages) claims(ctx context.Context,after uuid.UUID)([]databaseScanClaimEntry,error) {
 rows,err:=pages.queries.ListOwnedVulnerabilityDatabaseScanClaims(ctx,sqlc.ListOwnedVulnerabilityDatabaseScanClaimsParams{AfterID:after,PageLimit:32})
 if err!=nil{return nil,err}
 entries:=make([]databaseScanClaimEntry,len(rows));for index,row:=range rows {entries[index]=databaseScanClaimEntry{id:row.ID}}
 return entries,nil
}
func (pages databaseScanSQLPages) clusters(ctx context.Context,after uuid.UUID)([]uuid.UUID,error) {
 return pages.queries.ListOwnedDatabaseScanClusters(ctx,sqlc.ListOwnedDatabaseScanClustersParams{AfterID:after,PageLimit:32})
}

// The actual owner passes only its concrete SQL reader and fixed private methods.
// This scheduling seam grants no physical/transaction/verifier authority and is
// not stored in or selected by public owner/config/request/plugin state.
func progressOwnedDatabaseScans(ctx context.Context,pages databaseScanSweepReader,work databaseScanSweepWork,cursors *databaseScanCursors)error {
 if ctx==nil||pages==nil||work==nil||cursors==nil{return errors.New("owned database sweep unavailable")}
 // Existing claims have an independent budget and run before discovery. Safe
 // item failures are UNKNOWN and collected; they cannot starve following claims.
 claimsCtx,claimsStop:=context.WithTimeout(ctx,2*time.Minute)
 claims:=progressDatabaseClaimPages(claimsCtx,pages,work,&cursors.claims);claimsStop()
 if err:=ctx.Err();err!=nil{return err}
 discoveryCtx,discoveryStop:=context.WithTimeout(ctx,2*time.Minute)
 discovery:=progressDatabaseClusterPages(discoveryCtx,pages,work,&cursors.clusters);discoveryStop()
 if err:=ctx.Err();err!=nil{return err}
 return errors.Join(claims,discovery)
}
func progressDatabaseClaimPages(ctx context.Context,pages databaseScanSweepReader,work databaseScanSweepWork,after *uuid.UUID)error {
 var problems []error
 for page:=0;page<16;page++ {
  if canceled:=ctx.Err();canceled!=nil{return canceled}
  entries,err:=pages.claims(ctx,*after)
  if err!=nil{return errors.Join(append(problems,errors.New("owned database claim page unavailable"))...)}
  if len(entries)>32{return errors.Join(append(problems,errors.New("owned database claim page exceeds bound"))...)}
  for _,entry:=range entries {
   if entry.id==uuid.Nil||entry.id.String()<=after.String(){return errors.Join(append(problems,errors.New("owned database claim cursor invalid"))...)}
   err:=work.dispatchClaim(ctx,entry.id)
   // The fixed action has returned/joined. Advance even on its phase budget
   // expiration so one slow claim cannot permanently starve later claims.
   *after=entry.id
   if canceled:=ctx.Err();canceled!=nil{return canceled}
   if err!=nil&&!errors.Is(err,errDatabaseScanPending){problems=append(problems,databaseScanUnknownProgress(err,"owned database claim unknown"))}
  }
  if len(entries)<32{*after=uuid.Nil;return errors.Join(problems...)}
 }
 return errors.Join(append(problems,errors.New("owned database claim sweep incomplete"))...)
}
func progressDatabaseClusterPages(ctx context.Context,pages databaseScanSweepReader,work databaseScanSweepWork,after *uuid.UUID)error {
 var problems []error
 for page:=0;page<16;page++ {
  if canceled:=ctx.Err();canceled!=nil{return canceled}
  clusters,err:=pages.clusters(ctx,*after)
  if err!=nil{return errors.Join(append(problems,errors.New("owned database cluster page unavailable"))...)}
  if len(clusters)>32{return errors.Join(append(problems,errors.New("owned database cluster page exceeds bound"))...)}
  for _,cluster:=range clusters {
   if cluster==uuid.Nil||cluster.String()<=after.String(){return errors.Join(append(problems,errors.New("owned database cluster cursor invalid"))...)}
   err:=work.discoverCluster(ctx,cluster)
   *after=cluster
   if canceled:=ctx.Err();canceled!=nil{return canceled}
   if err!=nil{problems=append(problems,databaseScanUnknownProgress(err,"owned database cluster discovery unknown"))}
  }
  if len(clusters)<32{*after=uuid.Nil;return errors.Join(problems...)}
 }
 return errors.Join(append(problems,errors.New("owned database cluster sweep incomplete"))...)
}
