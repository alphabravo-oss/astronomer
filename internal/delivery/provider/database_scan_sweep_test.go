package provider

import (
 "context"
 "errors"
 "fmt"
 "strings"
 "testing"

 "github.com/google/uuid"
)

type ownedScanPageFixture struct {claimIDs,clusterIDs []uuid.UUID}
func scanPageFixtureIDs(count int)[]uuid.UUID {
 values:=make([]uuid.UUID,count)
 for index:=range values {values[index]=uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012x",index+1))}
 return values
}
func ownedScanFixturePage(ids []uuid.UUID,after uuid.UUID)[]uuid.UUID {
 begin:=0;for begin<len(ids)&&ids[begin].String()<=after.String(){begin++}
 end:=min(begin+32,len(ids));return ids[begin:end]
}
func (pages ownedScanPageFixture) claims(_ context.Context,after uuid.UUID)([]databaseScanClaimEntry,error) {
 ids:=ownedScanFixturePage(pages.claimIDs,after);entries:=make([]databaseScanClaimEntry,len(ids))
 for index,id:=range ids {entries[index]=databaseScanClaimEntry{id:id}};return entries,nil
}
func (pages ownedScanPageFixture) clusters(_ context.Context,after uuid.UUID)([]uuid.UUID,error) {return ownedScanFixturePage(pages.clusterIDs,after),nil}
type ownedScanWorkFixture struct {claims,clusters []uuid.UUID;failCluster,failClaim uuid.UUID;order []string}
func (work *ownedScanWorkFixture) dispatchClaim(_ context.Context,id uuid.UUID)error {
 work.claims=append(work.claims,id);work.order=append(work.order,"claim")
 if id==work.failClaim{return errors.New("private item diagnostic must not escape scheduler")};return nil
}
func (work *ownedScanWorkFixture) discoverCluster(_ context.Context,id uuid.UUID)error {
 work.clusters=append(work.clusters,id);work.order=append(work.order,"cluster")
 if id==work.failCluster{return errors.New("private supplier token must not escape scheduler")};return nil
}
func TestOwnedScanSweepProgressesExistingClaimDespiteUnrelatedDiscoveryFailure(t *testing.T) {
 ids:=scanPageFixtureIDs(2)
 work:=&ownedScanWorkFixture{failCluster:ids[0]};cursors:=&databaseScanCursors{}
 err:=progressOwnedDatabaseScans(t.Context(),ownedScanPageFixture{claimIDs:ids[:1],clusterIDs:ids},work,cursors)
 if err==nil||len(work.claims)!=1||work.claims[0]!=ids[0]||len(work.clusters)!=2||work.order[0]!="claim"||strings.Contains(err.Error(),"private supplier") {t.Fatal("unrelated unknown discovery starved claim, hid failure or leaked diagnostic")}
 if cursors.claims!=uuid.Nil||cursors.clusters!=uuid.Nil{t.Fatal("completed short pages left incorrect cursor")}
 work=&ownedScanWorkFixture{failClaim:ids[0]}
 if err:=progressOwnedDatabaseScans(t.Context(),ownedScanPageFixture{claimIDs:ids,clusterIDs:ids[:1]},work,&databaseScanCursors{});err==nil||len(work.claims)!=2||len(work.clusters)!=1 {t.Fatal("one invalid claim starved later claim or discovery")}
}
func TestOwnedScanSweepResumesBothBoundedFleetCursorsWithoutCleanCoverage(t *testing.T) {
 ids:=scanPageFixtureIDs(544);work:=&ownedScanWorkFixture{};cursors:=&databaseScanCursors{}
 pages:=ownedScanPageFixture{claimIDs:ids,clusterIDs:ids}
 if err:=progressOwnedDatabaseScans(t.Context(),pages,work,cursors);err==nil||!strings.Contains(err.Error(),"incomplete") {t.Fatal("bounded partial fleet was reported complete")}
 if len(work.claims)!=512||len(work.clusters)!=512||cursors.claims!=ids[511]||cursors.clusters!=ids[511] {t.Fatal("finite fair cursor budget differs")}
 if err:=progressOwnedDatabaseScans(t.Context(),pages,work,cursors);err!=nil {t.Fatal("second sweep failed to continue exact remaining page")}
 if len(work.claims)!=len(ids)||len(work.clusters)!=len(ids)||cursors.claims!=uuid.Nil||cursors.clusters!=uuid.Nil {t.Fatal("cursor restarted and starved later fleet")}
 for index,id:=range ids {if work.claims[index]!=id||work.clusters[index]!=id {t.Fatal("claim or cluster duplicated/dropped during continuation")}}
 canceled,stop:=context.WithCancel(t.Context());stop()
 if err:=progressOwnedDatabaseScans(canceled,pages,work,cursors);!errors.Is(err,context.Canceled) {t.Fatal("canceled fair sweep lost context identity")}
 if len(work.claims)!=544||len(work.clusters)!=544 {t.Fatal("canceled sweep performed work")}
}
