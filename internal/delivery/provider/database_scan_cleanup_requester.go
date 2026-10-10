package provider

import (
 "context"
 "encoding/json"
 "errors"
 "net/http"
 "reflect"
 "strings"

 "github.com/google/uuid"
 digest "github.com/opencontainers/go-digest"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/types"

 "github.com/alphabravocompany/astronomer-go/internal/callerid"
 "github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Separate private terminal-claim scope. Existing ordinary scan requesters
// still reject DELETE. Namespace, collections and arbitrary object names are
// never admitted, nor can request headers create this scope.
type databaseScanTerminalRequester struct {
 reads *databaseScanScopedRequester
 path,requestDigest string
}
func newDatabaseScanTerminalRequester(requester databaseScanRequester,cluster,claim uuid.UUID,path string,uid types.UID,version string)(*databaseScanTerminalRequester,[]byte,error) {
 if _,valid:=databaseScanResourceRole(claim,path);!valid||!validDatabaseScanUID(uid)||version==""||len(version)>128||strings.ContainsAny(version," \r\n\t") {return nil,nil,errors.New("owned terminal resource identity unavailable")}
 reads,err:=newDatabaseScanScopedRequester(requester,cluster,[]string{path},nil)
 if err!=nil{return nil,nil,err}
 zero:=int64(0);foreground:=metav1.DeletePropagationForeground
 body,err:=json.Marshal(metav1.DeleteOptions{TypeMeta:metav1.TypeMeta{APIVersion:"v1",Kind:"DeleteOptions"},Preconditions:&metav1.Preconditions{UID:&uid,ResourceVersion:&version},GracePeriodSeconds:&zero,PropagationPolicy:&foreground})
 if err!=nil{return nil,nil,errors.New("owned terminal delete options unavailable")}
 return &databaseScanTerminalRequester{reads:reads,path:path,requestDigest:digest.FromBytes(body).String()},body,nil
}
func (scope *databaseScanTerminalRequester) Do(ctx context.Context,cluster,method,path string,body []byte,headers map[string]string)(*protocol.K8sResponsePayload,error) {
 if scope==nil||scope.reads==nil{return nil,errors.New("owned terminal requester unavailable")}
 if method==http.MethodGet{return scope.reads.Do(ctx,cluster,method,path,body,headers)}
 if ctx==nil||ctx.Err()!=nil {if ctx!=nil{return nil,ctx.Err()};return nil,errors.New("owned terminal context unavailable")}
 identity:=callerid.Resolve(ctx);source,marked:=callerid.MachineSourceFromContext(ctx)
 if callerid.HasAuthenticatedUserContext(ctx)||!marked||source!=callerid.SourceDatabaseScan||!identity.IsMachine()||identity.User!=callerid.MachineSubject(callerid.SourceDatabaseScan)||cluster!=scope.reads.cluster.String()||method!=http.MethodDelete||path!=scope.path||len(body)==0||len(body)>4096||digest.FromBytes(body).String()!=scope.requestDigest||!reflect.DeepEqual(headers,map[string]string{"Content-Type":"application/json"}) {return nil,errors.New("owned terminal deletion outside exact recorded object")}
 return scope.reads.requester.Do(ctx,cluster,method,path,body,headers)
}
