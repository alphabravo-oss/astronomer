//go:build unix && integration

package provider

import (
 "context"
 "fmt"
 "math"
 "strings"
 "testing"
 "time"

 "github.com/google/uuid"
 "github.com/jackc/pgx/v5"

 "github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
 "github.com/alphabravocompany/astronomer-go/internal/scanner"
)

// These real transaction controls require the existing exact runner-created
// PG17/18 name/OID/lease/schema79 fence and genuine signed QA base fixture.
// They measure ordinary report observation transactions, not scan-used proof.
func TestDatabaseObservationPostgresAbsentInvalidAndOriginalPositiveFindings(t *testing.T) {
 ctx,cancel:=context.WithTimeout(t.Context(),30*time.Second)
 defer cancel()
 fixture,cluster,q,ingester,report:=databaseObservationFixture(t)
 for index,tc:=range []struct{name,clock,state string}{
  {"absent","","missing"}, {"invalid","invalid","invalid"},
  {"valid","2020-10-10T12:00:00Z","valid"},
 } {
  t.Run(tc.name,func(t *testing.T){
   report.Report.UpdateTimestamp=tc.clock
   before:=time.Now().UTC()
   if err:=ingester.Ingest(ctx,cluster,report);err!=nil {t.Fatal("real ordinary ingest failed")}
   id:=databaseObservationReportID(t,fixture,cluster,report.Metadata.Name)
   observation,err:=q.GetImageVulnerabilityObservation(ctx,id)
   if err!=nil||observation.Revision!=int64(index+1)||observation.TimeState!=tc.state||observation.UpstreamTime.Valid!=(tc.state=="valid") {t.Fatal("real observation lost nullable original clock or monotonic revision")}
   stored,err:=q.GetImageVulnerabilityReportByID(ctx,id)
   if err!=nil||stored.HighCount!=1 {t.Fatal("known summary disappeared")}
   if tc.state!="valid"&&(stored.ScannedAt.Before(before)||stored.ScannedAt.After(time.Now().UTC())) {t.Fatal("legacy receipt fallback changed")}
   databaseObservationAssertFindings(t,fixture,id)
   fixture.assertBackendReleased(t)
  })
 }
}

func TestDatabaseObservationPostgresRevisionSurvivesEqualAndBackwardsReceivedClock(t *testing.T) {
 ctx,cancel:=context.WithTimeout(t.Context(),30*time.Second)
 defer cancel()
 for _,delta:=range []string{"equal","backwards"} {
  t.Run(delta,func(t *testing.T){
   fixture,cluster,q,ingester,report:=databaseObservationFixture(t)
   if err:=ingester.Ingest(ctx,cluster,report);err!=nil {t.Fatal("initial real ordinary ingest failed")}
   id:=databaseObservationReportID(t,fixture,cluster,report.Metadata.Name)
   previous,err:=q.GetImageVulnerabilityObservation(ctx,id)
   if err!=nil {t.Fatal("initial observation missing")}
   // A private test-only trigger changes only this randomly owned report's
   // received clock. It is never installed by production code. This gives a
   // deterministic real equal/backwards-clock control without changing the host
   // clock, upstream timestamps, or canonical observation SQL/revision logic.
   databaseObservationInstallClockFixture(t,fixture,id,delta)
   if err:=ingester.Ingest(ctx,cluster,report);err!=nil {t.Fatal("equal-clock real ordinary ingest failed")}
   current,err:=q.GetImageVulnerabilityObservation(ctx,id)
   if err!=nil||current.Revision!=previous.Revision+1 {t.Fatal("observation revision reused after clock adjustment")}
   expected:=previous.ReceivedAt
   if delta=="backwards" {expected=expected.Add(-time.Second)}
   if !current.ReceivedAt.Equal(expected)||!current.UpstreamTime.Time.Equal(previous.UpstreamTime.Time) {t.Fatal("clock control did not exercise intended equal/backwards receipt")}
   databaseObservationAssertFindings(t,fixture,id)
   fixture.assertBackendReleased(t)
  })
 }
}

func TestDatabaseObservationPostgresOverflowRollsBackReportAndCVEs(t *testing.T) {
 ctx,cancel:=context.WithTimeout(t.Context(),30*time.Second)
 defer cancel()
 fixture,cluster,q,ingester,report:=databaseObservationFixture(t)
 if err:=ingester.Ingest(ctx,cluster,report);err!=nil {t.Fatal("initial real ordinary ingest failed")}
 id:=databaseObservationReportID(t,fixture,cluster,report.Metadata.Name)
 if _,err:=fixture.pool.Exec(ctx,"UPDATE public.image_vulnerability_observations SET revision=$2 WHERE report_id=$1",id,int64(math.MaxInt64));err!=nil {t.Fatal("private overflow fixture setup failed")}
 original,err:=q.GetImageVulnerabilityReportByID(ctx,id)
 if err!=nil {t.Fatal("original real report missing")}
 changed:=report
 changed.Metadata.UID="changed-uid"
 changed.Report.UpdateTimestamp="2020-10-11T12:00:00Z"
 changed.Report.Summary.HighCount=0
 changed.Report.Vulnerabilities=nil
 if err:=ingester.Ingest(ctx,cluster,changed);err==nil {t.Fatal("revision overflow accepted and could replace known findings")}
 current,err:=q.GetImageVulnerabilityReportByID(ctx,id)
 if err!=nil||current.HighCount!=original.HighCount||!current.ScannedAt.Equal(original.ScannedAt) {t.Fatal("observation error did not roll back actual report update")}
 observation,err:=q.GetImageVulnerabilityObservation(ctx,id)
 if err!=nil||observation.Revision!=math.MaxInt64||observation.UpstreamReportUid!=report.Metadata.UID {t.Fatal("failed revision was partially committed or reset")}
 databaseObservationAssertFindings(t,fixture,id)
 fixture.assertBackendReleased(t)
}

func TestDatabaseObservationPostgresConcurrentOrdinaryIngestSerializesRevision(t *testing.T) {
 ctx,cancel:=context.WithTimeout(t.Context(),30*time.Second)
 defer cancel()
 fixture,cluster,q,ingester,report:=databaseObservationFixture(t)
 if err:=ingester.Ingest(ctx,cluster,report);err!=nil {t.Fatal("initial real ordinary ingest failed")}
 const count=8
 results:=make(chan error,count)
 joined:=0
 t.Cleanup(func(){
  cancel()
  deadline:=time.NewTimer(7*time.Second)
  defer deadline.Stop()
  for joined<count {
   select {
   case <-results:joined++
   case <-deadline.C:t.Error("concurrent actual ingest worker remains unjoined");return
   }
  }
 })
 for range count {go func(){results<-ingester.Ingest(ctx,cluster,report)}()}
 for joined<count {
  select {
  case err:=<-results:
   joined++
   if err!=nil {t.Error("concurrent actual ingest failed")}
  case <-ctx.Done():t.Fatal("concurrent actual ingest deadline exceeded; cleanup will explicitly join")
  }
 }
 id:=databaseObservationReportID(t,fixture,cluster,report.Metadata.Name)
 observation,err:=q.GetImageVulnerabilityObservation(ctx,id)
 if err!=nil||observation.Revision!=count+1 {t.Fatal("equal upstream timestamps collapsed observation revisions")}
 databaseObservationAssertFindings(t,fixture,id)
 fixture.assertBackendReleased(t)
}

func databaseObservationFixture(t *testing.T)(*databaseRestartPostgresFixture,uuid.UUID,*sqlc.Queries,*scanner.Ingester,scanner.TrivyVulnerabilityReport) {
 t.Helper()
 fixture:=openDatabaseRestartPostgresFixture(t)
 q:=sqlc.New(fixture.pool)
 createContext,stopCreate:=context.WithTimeout(t.Context(),5*time.Second)
 defer stopCreate()
 cluster:=createDatabaseRefreshCluster(t,createContext,q)
 t.Cleanup(func(){
  ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
  defer cancel()
  if err:=q.DeleteCluster(ctx,cluster);err!=nil {t.Error("exact privately created observation cluster cleanup failed")}
 })
 report:=scanner.TrivyVulnerabilityReport{
  Metadata:scanner.TrivyMetadata{Name:"plan032-observation-"+uuid.NewString(),UID:uuid.NewString(),Namespace:"default"},
  Report:scanner.TrivyReport{Scanner:scanner.TrivyScanner{Name:"Trivy",Version:"fixture"},Artifact:scanner.TrivyArtifact{Repository:"fixture",Digest:"sha256:"+strings.Repeat("a",64)},Summary:scanner.TrivySummary{HighCount:1},UpdateTimestamp:"2020-10-10T12:00:00Z",Vulnerabilities:[]scanner.TrivyCVE{{VulnerabilityID:"CVE-2026-12345",Severity:"HIGH",Resource:"package",InstalledVersion:"1",FixedVersion:"2"}}},
 }
 return fixture,cluster,q,scanner.NewIngester(q,fixture.pool,nil,nil),report
}

func databaseObservationReportID(t *testing.T,fixture *databaseRestartPostgresFixture,cluster uuid.UUID,name string) uuid.UUID {
 t.Helper()
 ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
 defer cancel()
 var id uuid.UUID
 if err:=fixture.pool.QueryRow(ctx,"SELECT id FROM public.image_vulnerability_reports WHERE cluster_id=$1 AND report_name=$2",cluster,name).Scan(&id);err!=nil {t.Fatal("actual owned report missing")}
 return id
}

func databaseObservationAssertFindings(t *testing.T,fixture *databaseRestartPostgresFixture,report uuid.UUID) {
 t.Helper()
 ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
 defer cancel()
 var findings int
 if err:=fixture.pool.QueryRow(ctx,"SELECT count(*) FROM public.image_vulnerabilities WHERE report_id=$1 AND vulnerability_id='CVE-2026-12345' AND severity='HIGH'",report).Scan(&findings);err!=nil||findings!=1 {t.Fatal("known real positive finding disappeared or duplicated")}
 // These ordinary ingest cases have no owned Job witness. Their received
 // clock, report UID and successful transaction may not create one.
 var witnesses int
 if err:=fixture.pool.QueryRow(ctx,"SELECT count(*) FROM public.image_vulnerability_database_witnesses WHERE report_id=$1",report).Scan(&witnesses);err!=nil||witnesses!=0 {t.Fatal("ordinary ingest fabricated scan-used witness")}
}

func databaseObservationInstallClockFixture(t *testing.T,fixture *databaseRestartPostgresFixture,report uuid.UUID,delta string) {
 t.Helper()
 suffix:=strings.ReplaceAll(uuid.NewString(),"-","")
 function:=pgx.Identifier{"public","plan032_w04_clock_"+suffix}.Sanitize()
 trigger:=pgx.Identifier{"plan032_w04_clock_"+suffix}.Sanitize()
 adjustment:="OLD.received_at"
 if delta=="backwards" {adjustment+=" - interval '1 second'"} else if delta!="equal" {t.Fatal("unsupported private clock fixture")}
 // UUID and identifiers come only from typed locally generated fixture values.
 body:=fmt.Sprintf("CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $clock$ BEGIN IF NEW.report_id='%s'::uuid THEN NEW.received_at := %s; END IF; RETURN NEW; END; $clock$",function,report.String(),adjustment)
 t.Cleanup(func(){
  ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
  defer cancel()
  if _,err:=fixture.pool.Exec(ctx,"DROP TRIGGER IF EXISTS "+trigger+" ON public.image_vulnerability_observations");err!=nil {t.Error("private clock trigger cleanup failed")}
  if _,err:=fixture.pool.Exec(ctx,"DROP FUNCTION IF EXISTS "+function+"()");err!=nil {t.Error("private clock function cleanup failed")}
 })
 ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second)
 defer cancel()
 if _,err:=fixture.pool.Exec(ctx,body);err!=nil {t.Fatal("private clock function setup failed")}
 if _,err:=fixture.pool.Exec(ctx,"CREATE TRIGGER "+trigger+" BEFORE UPDATE ON public.image_vulnerability_observations FOR EACH ROW EXECUTE FUNCTION "+function+"()");err!=nil {t.Fatal("private clock trigger setup failed")}
}
