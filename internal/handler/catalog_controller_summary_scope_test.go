package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

type catalogControllerSummaryQuerier struct {
	*minimalCatalogQuerier
	rows []sqlc.CatalogOperation
	globalCalls,scopeCalls,installedCalls int
	chartError,installedError,countError error
	countOverride *int64
}

func (q *catalogControllerSummaryQuerier) ListCatalogOperations(_ context.Context,p sqlc.ListCatalogOperationsParams)([]sqlc.CatalogOperation,error) {
	q.globalCalls++
	limit:=int(p.Limit)
	if limit<0 || p.Offset!=0 {return nil,errors.New("owned catalog summary window changed")}
	if limit>len(q.rows) {limit=len(q.rows)}
	return q.rows[:limit],nil
}

func (q *catalogControllerSummaryQuerier) CountCatalogOperations(context.Context,sqlc.CountCatalogOperationsParams)(int64,error) {
	if q.countOverride!=nil {return *q.countOverride,q.countError}
	return int64(len(q.rows)),q.countError
}

func (q *catalogControllerSummaryQuerier) scopedRows(ids []uuid.UUID) []sqlc.CatalogOperation {
	out:=[]sqlc.CatalogOperation{}
	for _,op:=range q.rows {
		id,err:=catalogOperationClusterID(op)
		if err!=nil {continue}
		for _,allowed:=range ids {
			if id==allowed {out=append(out,op);break}
		}
	}
	return out
}

func (q *catalogControllerSummaryQuerier) ListCatalogOperationsForScopes(_ context.Context,p sqlc.ListCatalogOperationsForScopesParams)([]sqlc.CatalogOperation,error) {
	q.scopeCalls++
	rows:=q.scopedRows(p.ClusterIds)
	limit:=int(p.QueryLimit)
	if limit<0 || p.QueryOffset!=0 {return nil,errors.New("owned scoped catalog summary window changed")}
	if limit>len(rows) {limit=len(rows)}
	return rows[:limit],nil
}

func (q *catalogControllerSummaryQuerier) CountCatalogOperationsForScopes(_ context.Context,p sqlc.CountCatalogOperationsForScopesParams)(int64,error) {
	if q.countOverride!=nil {return *q.countOverride,q.countError}
	return int64(len(q.scopedRows(p.ClusterIds))),q.countError
}

func (q *catalogControllerSummaryQuerier) CountHelmCharts(context.Context)(int64,error) {return 7,q.chartError}
func (q *catalogControllerSummaryQuerier) CountInstalledCharts(context.Context)(int64,error) {q.installedCalls++;return 9,q.installedError}

func catalogControllerSummaryOperation(t *testing.T,cluster uuid.UUID,status string) sqlc.CatalogOperation {
	t.Helper()
	payload,err:=json.Marshal(catalogOperationEnvelope{ClusterID:cluster.String()})
	if err!=nil {t.Fatal("owned operation payload setup failed")}
	return sqlc.CatalogOperation{ID:uuid.New(),Payload:payload,Status:status,CreatedAt:time.Now().UTC()}
}

func catalogControllerScopedHandler(q *catalogControllerSummaryQuerier,cluster uuid.UUID) *CatalogHandler {
	h:=NewCatalogHandler(q)
	h.SetAuthorization(rbac.NewEngine(),stubMonitoringRBACQuerier{bindings:catalogReadBindings(cluster)})
	return h
}

func TestCatalogControllerSummaryScopedWindowAndUnknownCounts(t *testing.T) {
	t.Run("authorized-after-global1000",func(t *testing.T) {
		allowed,other:=uuid.New(),uuid.New()
		q:=&catalogControllerSummaryQuerier{minimalCatalogQuerier:&minimalCatalogQuerier{}}
		for i:=0;i<1000;i++ {q.rows=append(q.rows,catalogControllerSummaryOperation(t,other,"completed"))}
		late:=catalogControllerSummaryOperation(t,allowed,"failed");q.rows=append(q.rows,late)
		h:=catalogControllerScopedHandler(q,allowed)
		summary,err:=h.controllerSummary(authedCatalogReq(http.MethodGet,"/api/v1/catalog/controller/status/",nil).Context())
		if err!=nil || summary["operations"].(map[string]int)["failed"]!=1 || q.scopeCalls!=1 || q.globalCalls!=0 {
			t.Fatal("catalog summary applied authorization after the global1000 operation window")
		}
		failure,ok:=summary["latestFailure"].(map[string]any)
		if !ok || failure["id"]!=late.ID.String() {t.Fatal("catalog summary omitted the authorized late failure")}
	})
	t.Run("1001-window-completeness",func(t *testing.T) {
		cluster:=uuid.New();q:=&catalogControllerSummaryQuerier{minimalCatalogQuerier:&minimalCatalogQuerier{}}
		for i:=0;i<1001;i++ {q.rows=append(q.rows,catalogControllerSummaryOperation(t,cluster,"pending"))}
		summary,err:=NewCatalogHandler(q).controllerSummary(context.Background())
		window,ok:=summary["operationSummary"].(map[string]any)
		if err!=nil || !ok || window["complete"]!=false || window["windowLimit"]!=1000 || window["returned"]!=1000 || window["total"]!=int64(1001) {
			t.Fatal("catalog summary presented a1000-row window as an unqualified complete count")
		}
		if summary["operations"].(map[string]int)["pending"]!=1000 {t.Fatal("legacy catalog window count changed")}
	})
	for _,tc:=range []struct{name string;charts bool}{{"chart-count-error",true},{"installed-count-error",false}} {
		t.Run(tc.name,func(t *testing.T) {
			q:=&catalogControllerSummaryQuerier{minimalCatalogQuerier:&minimalCatalogQuerier{}}
			if tc.charts {q.chartError=errors.New("owned count unavailable")} else {q.installedError=errors.New("owned count unavailable")}
			w:=httptest.NewRecorder()
			NewCatalogHandler(q).ControllerStatus(w,httptest.NewRequest(http.MethodGet,"/api/v1/catalog/controller/status/",nil))
			if w.Code!=http.StatusInternalServerError {t.Fatal("catalog summary concealed unknown inventory count as successful zero")}
		})
	}
	t.Run("restricted-installed-count-is-not-read",func(t *testing.T) {
		cluster:=uuid.New();q:=&catalogControllerSummaryQuerier{minimalCatalogQuerier:&minimalCatalogQuerier{},installedError:errors.New("owned global installed count must not be read")}
		h:=catalogControllerScopedHandler(q,cluster)
		summary,err:=h.controllerSummary(authedCatalogReq(http.MethodGet,"/api/v1/catalog/controller/status/",nil).Context())
		if err!=nil || q.installedCalls!=0 || summary["catalog"].(map[string]any)["installedCount"]!=nil {t.Fatal("restricted catalog summary read the hidden global installed count")}
	})
	t.Run("live-scope-A-B-A",func(t *testing.T) {
		a,b:=uuid.New(),uuid.New();q:=&catalogControllerSummaryQuerier{minimalCatalogQuerier:&minimalCatalogQuerier{}}
		q.rows=[]sqlc.CatalogOperation{catalogControllerSummaryOperation(t,a,"failed"),catalogControllerSummaryOperation(t,b,"pending")}
		h:=catalogControllerScopedHandler(q,a)
		for _,cluster:=range []uuid.UUID{a,b,a} {
			h.SetAuthorization(rbac.NewEngine(),stubMonitoringRBACQuerier{bindings:catalogReadBindings(cluster)})
			summary,err:=h.controllerSummary(authedCatalogReq(http.MethodGet,"/api/v1/catalog/controller/status/",nil).Context())
			if err!=nil {t.Fatal("scoped catalog summary changed available-read behavior")}
			ops:=summary["operations"].(map[string]int)
			if cluster==a && (ops["failed"]!=1 || ops["pending"]!=0) || cluster==b && (ops["pending"]!=1 || ops["failed"]!=0) {t.Fatal("catalog summary retained another live actor scope")}
		}
		if q.globalCalls!=0 || q.scopeCalls!=3 {t.Fatal("catalog summary did not reauthorize every scoped window")}
	})

	for _,scoped:=range []bool{false,true} {
		name:="global-operation-count-error";if scoped {name="scoped-operation-count-error"}
		t.Run(name,func(t *testing.T) {
			cluster:=uuid.New();q:=&catalogControllerSummaryQuerier{minimalCatalogQuerier:&minimalCatalogQuerier{},countError:errors.New("owned operation count unavailable")}
			h:=NewCatalogHandler(q);ctx:=context.Background();if scoped {h=catalogControllerScopedHandler(q,cluster);ctx=authedCatalogReq(http.MethodGet,"/api/v1/catalog/controller/status/",nil).Context()}
			if _,err:=h.controllerSummary(ctx);err==nil {t.Fatal("catalog summary concealed an unknown operation count")}
		})
	}
	t.Run("negative-operation-count",func(t *testing.T) {
		negative:=int64(-1);q:=&catalogControllerSummaryQuerier{minimalCatalogQuerier:&minimalCatalogQuerier{},countOverride:&negative}
		if _,err:=NewCatalogHandler(q).controllerSummary(context.Background());err==nil {t.Fatal("catalog summary accepted an impossible negative operation count")}
	})
	t.Run("cancelled-before-query",func(t *testing.T) {
		q:=&catalogControllerSummaryQuerier{minimalCatalogQuerier:&minimalCatalogQuerier{}};ctx,cancel:=context.WithCancel(context.Background());cancel()
		_,err:=NewCatalogHandler(q).controllerSummary(ctx)
		if !errors.Is(err,context.Canceled) || q.globalCalls!=0 || q.scopeCalls!=0 || q.installedCalls!=0 {t.Fatal("catalog summary queried after request cancellation")}
	})
	for _,narrow:=range []string{"namespace","project"} {
		t.Run(narrow+"-binding-does-not-widen",func(t *testing.T) {
			cluster:=uuid.New();q:=&catalogControllerSummaryQuerier{minimalCatalogQuerier:&minimalCatalogQuerier{}}
			q.rows=[]sqlc.CatalogOperation{catalogControllerSummaryOperation(t,cluster,"failed")};bindings:=catalogReadBindings(cluster)
			if narrow=="namespace" {bindings[0].Namespace="owned-namespace"} else {bindings[0].ProjectID=uuid.NewString()}
			h:=NewCatalogHandler(q);h.SetAuthorization(rbac.NewEngine(),stubMonitoringRBACQuerier{bindings:bindings})
			summary,err:=h.controllerSummary(authedCatalogReq(http.MethodGet,"/api/v1/catalog/controller/status/",nil).Context())
			if err!=nil || q.globalCalls!=0 || q.scopeCalls!=1 || len(summary["operations"].(map[string]int))!=0 {t.Fatal("catalog summary widened a namespace or project binding into a cluster operation window")}
		})
	}
	t.Run("empty-compatible-shape",func(t *testing.T) {
		q:=&catalogControllerSummaryQuerier{minimalCatalogQuerier:&minimalCatalogQuerier{}}
		summary,err:=NewCatalogHandler(q).controllerSummary(context.Background())
		if err!=nil || summary["catalog"].(map[string]any)["chartCount"]!=int64(7) || summary["catalog"].(map[string]any)["installedCount"]!=int64(9) || len(summary["operations"].(map[string]int))!=0 {t.Fatal("empty catalog summary legacy fields changed")}
	})
}
