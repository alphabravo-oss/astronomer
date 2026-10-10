package provider

import (
 "testing"
 "errors"
 "strings"
 "github.com/alphabravocompany/astronomer-go/internal/callerid"
)

func TestOwnedScanCapacityRefusesUnresolvedAllocationsAndAllowsOnlyExistingClaimReuse(t *testing.T) {
 for _,test:=range []struct{name string;count int64;existing,allowed bool}{
  {"empty",0,false,true},{"last-slot",31,false,true},
  {"unknown-terminal-counted",32,false,false},{"existing-current-claim",32,true,true},
  {"negative-count",-1,true,false},{"invalid-over-cap",33,true,false},
 } {
  t.Run(test.name,func(t *testing.T){if databaseScanAllocationAvailable(test.count,test.existing)!=test.allowed{t.Fatal("allocation cap was bypassed or current same-claim reuse was refused")}})
 }
}

func TestOwnedScanCapacityStatusSurvivesOnlyFixedCategoryAndNeverGrantsCleanup(t *testing.T) {
 got:=databaseScanUnknownProgress(errors.Join(errors.New("opaque supplier diagnostic"),errDatabaseScanResourceCapacity),"owned database workload item unknown")
 if !errors.Is(got,errDatabaseScanResourceCapacity)||strings.Contains(got.Error(),"supplier") {t.Fatal("machine-visible exhaustion was lost or raw diagnostic escaped")}
 arbitrary:=errors.New("opaque supplier diagnostic")
 got=databaseScanUnknownProgress(arbitrary,"owned database workload item unknown")
 if errors.Is(got,arbitrary)||got.Error()!="owned database workload item unknown" {t.Fatal("arbitrary child error became machine authority or leaked")}
 claim,resource,peer:=terminalResourceFixture();resource.ResourceUid.Valid=false;peer.missing=true
 if err:=removeTerminalDatabaseResource(callerid.WithMachine(t.Context(),callerid.SourceDatabaseScan),peer,claim,resource);err==nil||peer.deletes!=0 {t.Fatal("unknown absent UID freed allocation or granted deletion")}
}
