package provider

import (
 "context"
 "encoding/base64"
 "encoding/json"
 digest "github.com/opencontainers/go-digest"
 "errors"
 "fmt"
 "io"
 "net/url"
 "reflect"
 "strings"
 "time"

 "github.com/google/uuid"
 batchv1 "k8s.io/api/batch/v1"
 corev1 "k8s.io/api/core/v1"
 "k8s.io/apimachinery/pkg/types"
 "k8s.io/apimachinery/pkg/util/validation"

 "github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
 "github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
 "github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const databaseScanMaximumReportBytes = 8 << 20
const databaseScanMaximumAPIBytes = 2 << 20

// Internal read-only tunnel shape. The production owner uses the same existing
// tunnel requester; an HTTP caller cannot choose cluster/object identity here.
type databaseScanRequester interface {
 Do(context.Context,string,string,string,[]byte,map[string]string)(*protocol.K8sResponsePayload,error)
}

// These are DATA from the owner's actual created-object readback and durable
// claim, not identities accepted from a report, annotation or helper log.
type databaseScanObservationRequest struct {
 cluster uuid.UUID
 job *batchv1.Job
 jobUID, podUID types.UID
 podName string
 prepared databasejob.Preparation
}

type databaseScanObservation struct {
 jobUID,podUID types.UID
 scannerImageID,helperImageID string
 helperImageIDs []string
 runtimeDigest string
 scannerStarted,scannerFinished,helperFinished time.Time
 measurements databasejob.OrderedMeasurements
 report []byte
}

// Native scanner report bytes can contain private image configuration. Even
// private/unexported fields are printable by fmt unless every verb is covered.
func (databaseScanObservation) Format(state fmt.State,_ rune) {
 _,_=io.WriteString(state,"<owned database scan observation>")
}
func (observation *databaseScanObservation) close() {
 if observation!=nil {clear(observation.report);observation.report=nil}
}

var errDatabaseScanGone=errors.New("owned database scan API object or snapshot gone")
var errDatabaseScanPending=errors.New("owned database scan runtime still pending")

// observeDatabaseScan uses actual current Kubernetes reads, including a final
// post-log readback. It returns comparison DATA only. The private provider must
// still compare selected image/config IDs through the SAME current authority,
// re-read the actual workload/node and commit report/witness under locked079.
// A complete Job or a helper measurement cannot authorize that transaction.
func observeDatabaseScan(ctx context.Context, requester databaseScanRequester, request databaseScanObservationRequest)(databaseScanObservation,error) {
 var empty databaseScanObservation
 if ctx==nil||requester==nil||request.cluster==uuid.Nil||request.job==nil||request.job.Namespace!=databasejob.ScanNamespace||len(validation.IsDNS1123Subdomain(request.job.Name))!=0||request.job.Name==""||!validDatabaseScanUID(request.jobUID)||!validDatabaseScanUID(request.podUID)||request.podName==""||len(validation.IsDNS1123Subdomain(request.podName))!=0||request.prepared.SchemaVersion!=2||request.prepared.Workload==nil {return empty,errors.New("owned database scan observation inputs invalid")}
 bounded,cancel:=context.WithTimeout(ctx,60*time.Second);defer cancel()
 job,pod,err:=readDatabaseScanObjects(bounded,requester,request)
 if err!=nil{return empty,err}
 status,err:=databaseScanCompletedStatuses(job,pod)
 if err!=nil{return empty,err}
 helperLogs,err:=databaseScanGetBytes(bounded,requester,request.cluster,databaseScanPodPath(pod.Name)+"/log?container=observe&timestamps=true&limitBytes=33025",2*(databasejob.MaximumRecordBytes+128))
 if err!=nil{return empty,err}
 defer clear(helperLogs)
 measurements,err:=databasejob.DecodeOrderedMeasurements(helperLogs,request.prepared,status.scannerStarted,status.scannerFinished,status.helperFinished)
 if err!=nil{return empty,errors.New("owned database scan final observation unavailable")}
 report,err:=databaseScanGetBytes(bounded,requester,request.cluster,databaseScanPodPath(pod.Name)+"/log?container=scanner&timestamps=false&limitBytes=8388609",databaseScanMaximumReportBytes)
 if err!=nil{return empty,err}
 retained:=false;defer func(){if !retained {clear(report)}}()
 finalJob,finalPod,err:=readDatabaseScanObjects(bounded,requester,request)
 if err!=nil{return empty,err}
 final,err:=databaseScanCompletedStatuses(finalJob,finalPod)
 if err!=nil||!reflect.DeepEqual(final,status) {return empty,errors.New("owned database scan changed after log retrieval")}
 if err:=bounded.Err();err!=nil{return empty,err}
 retained=true
 return databaseScanObservation{jobUID:job.UID,podUID:pod.UID,scannerImageID:status.scannerImageID,helperImageID:status.helperImageID,helperImageIDs:append([]string(nil),status.helperImageIDs...),runtimeDigest:status.runtimeDigest,scannerStarted:status.scannerStarted,scannerFinished:status.scannerFinished,helperFinished:status.helperFinished,measurements:measurements,report:report},nil
}
func validDatabaseScanUID(value types.UID)bool {
 parsed,err:=uuid.Parse(string(value));return err==nil&&parsed!=uuid.Nil&&parsed.String()==string(value)
}
func databaseScanPodPath(name string)string {return "/api/v1/namespaces/"+databasejob.ScanNamespace+"/pods/"+url.PathEscape(name)}
func readDatabaseScanObjects(ctx context.Context,requester databaseScanRequester,request databaseScanObservationRequest)(*batchv1.Job,*corev1.Pod,error) {
 jobPath:="/apis/batch/v1/namespaces/"+databasejob.ScanNamespace+"/jobs/"+url.PathEscape(request.job.Name)
 jobBytes,err:=databaseScanGetBytes(ctx,requester,request.cluster,jobPath,databaseScanMaximumAPIBytes)
 if err!=nil{return nil,nil,err};defer clear(jobBytes)
 var job batchv1.Job
 if jsoncontract.Decode(jobBytes,&job)!=nil||job.APIVersion!="batch/v1"||job.Kind!="Job"||job.UID!=request.jobUID||job.Name!=request.job.Name||job.Namespace!=databasejob.ScanNamespace||job.DeletionTimestamp!=nil||!databaseScanJobSpecMatches(request.job,&job)||!databaseScanExpectedLabels(job.Spec.Template.Labels,request.job.Spec.Template.Labels) {return nil,nil,errors.New("owned database scan Job UID or critical spec changed")}
 selector:=url.QueryEscape("batch.kubernetes.io/controller-uid="+string(request.jobUID))
 podBytes,err:=databaseScanGetBytes(ctx,requester,request.cluster,"/api/v1/namespaces/"+databasejob.ScanNamespace+"/pods?labelSelector="+selector+"&limit=2",databaseScanMaximumAPIBytes)
 if err!=nil{return nil,nil,err};defer clear(podBytes)
 var pods corev1.PodList
 if jsoncontract.Decode(podBytes,&pods)!=nil||pods.APIVersion!="v1"||pods.Kind!="PodList"||pods.Continue!=""||len(pods.Items)!=1 {return nil,nil,errors.New("owned database scan single Pod readback unavailable")}
 pod:=&pods.Items[0]
 if err:=normalizeDatabasePodListItem(pod);err!=nil{return nil,nil,err}
 if pod.APIVersion!="v1"||pod.Kind!="Pod"||pod.UID!=request.podUID||pod.Name!=request.podName||len(validation.IsDNS1123Subdomain(pod.Name))!=0||pod.Namespace!=databasejob.ScanNamespace||pod.DeletionTimestamp!=nil||!databaseScanOwnedPod(pod,&job)||!databaseScanPodSpecMatches(request.job.Spec.Template.Spec,pod.Spec)||!databaseScanExpectedLabels(pod.Labels,request.job.Spec.Template.Labels) {return nil,nil,errors.New("owned database scan Pod UID or critical spec changed")}
 return &job,pod,nil
}
func databaseScanOwnedPod(pod *corev1.Pod,job *batchv1.Job)bool {
 if len(pod.OwnerReferences)!=1{return false}
 owner:=pod.OwnerReferences[0]
 return owner.APIVersion=="batch/v1"&&owner.Kind=="Job"&&owner.Name==job.Name&&owner.UID==job.UID&&owner.Controller!=nil&&*owner.Controller
}
func databaseScanExpectedLabels(actual,expected map[string]string)bool {
 for name,value:=range expected {if actual[name]!=value{return false}};return true
}

// Compare the exact canonical builder's critical fields. Server defaults such
// as scheduler/DNS/service-account name are not a second assignment compiler.
// Additional containers, mounts, env/secret references, privileges or volumes
// are refused. Actual CNI egress enforcement remains a separate prerequisite.
func databaseScanPodSpecMatches(expected,actual corev1.PodSpec)bool {
 if expected.NodeName==""||expected.NodeName!=actual.NodeName||!reflect.DeepEqual(expected.NodeSelector,actual.NodeSelector)||!reflect.DeepEqual(expected.AutomountServiceAccountToken,actual.AutomountServiceAccountToken)||actual.AutomountServiceAccountToken==nil||*actual.AutomountServiceAccountToken||expected.RestartPolicy!=actual.RestartPolicy||!reflect.DeepEqual(expected.TerminationGracePeriodSeconds,actual.TerminationGracePeriodSeconds)||!reflect.DeepEqual(expected.SecurityContext,actual.SecurityContext)||!reflect.DeepEqual(expected.Volumes,actual.Volumes)||len(expected.Containers)!=len(actual.Containers)||len(expected.InitContainers)!=len(actual.InitContainers)||len(actual.EphemeralContainers)!=0||actual.HostNetwork||actual.HostPID||actual.HostIPC||actual.ShareProcessNamespace!=nil&&*actual.ShareProcessNamespace||len(actual.ImagePullSecrets)!=len(expected.ImagePullSecrets) {return false}
 if !reflect.DeepEqual(expected.ImagePullSecrets,actual.ImagePullSecrets){return false}
 for index:=range expected.Containers {if !databaseScanContainerMatches(expected.Containers[index],actual.Containers[index]) {return false}}
 for index:=range expected.InitContainers {if !databaseScanContainerMatches(expected.InitContainers[index],actual.InitContainers[index]) {return false}}
 return true
}
func databaseScanContainerMatches(expected,actual corev1.Container)bool {
 // Kubernetes inserts only these two documented defaults in this controlled
 // shape. Everything else remains exact rather than dropping runtime fields.
 if expected.TerminationMessagePath==""&&actual.TerminationMessagePath=="/dev/termination-log" {actual.TerminationMessagePath=""}
 if expected.TerminationMessagePolicy==""&&actual.TerminationMessagePolicy==corev1.TerminationMessageReadFile {actual.TerminationMessagePolicy=""}
 if expected.ImagePullPolicy==""&&actual.ImagePullPolicy==corev1.PullIfNotPresent&&strings.Contains(expected.Image,"@sha256:") {actual.ImagePullPolicy=""}
 if !databaseScanResourceListsMatch(expected.Resources.Requests,actual.Resources.Requests)||!databaseScanResourceListsMatch(expected.Resources.Limits,actual.Resources.Limits) {return false}
 // Quantity serialization may change cached representation without changing
 // value. Extra/different requests or limits remain rejected by exact key/Cmp.
 value:=actual.DeepCopy();value.Resources.Requests=expected.Resources.Requests;value.Resources.Limits=expected.Resources.Limits
 return reflect.DeepEqual(expected,*value)
}

type databaseScanStatus struct {
 scannerImageID,helperImageID string
 helperImageIDs []string
 runtimeDigest string
 scannerStarted,scannerFinished,helperFinished time.Time
}
func databaseScanCompletedStatuses(job *batchv1.Job,pod *corev1.Pod)(databaseScanStatus,error) {
 var empty databaseScanStatus
 if job.Status.Failed!=0||pod.Status.Phase==corev1.PodFailed {return empty,errors.New("owned database scan runtime failed")}
 complete:=false
 for _,condition:=range job.Status.Conditions {if condition.Type==batchv1.JobFailed&&condition.Status==corev1.ConditionTrue{return empty,errors.New("owned database scan Job failed")};if condition.Type==batchv1.JobComplete&&condition.Status==corev1.ConditionTrue{complete=true}}
 if !complete||job.Status.Succeeded!=1||job.Status.Active!=0||pod.Status.Phase!=corev1.PodSucceeded {return empty,errDatabaseScanPending}
 if len(pod.Status.ContainerStatuses)!=1||len(pod.Status.InitContainerStatuses)!=len(pod.Spec.InitContainers) {return empty,errors.New("owned database scan runtime status incomplete")}
 scanner:=pod.Status.ContainerStatuses[0]
 if scanner.Name!="scanner"||scanner.Image!=pod.Spec.Containers[0].Image||!databaseScanTerminatedCleanly(scanner) {return empty,errors.New("owned database scanner termination invalid")}
 var helper corev1.ContainerStatus
 for index,container:=range pod.Spec.InitContainers {
  status:=pod.Status.InitContainerStatuses[index]
  if status.Name!=container.Name||status.Image!=container.Image||!databaseScanTerminatedCleanly(status) {return empty,errors.New("owned database init or observer termination invalid")}
  if container.Name=="observe" {helper=status}
 }
 if helper.Name!="observe"||helper.ImageID=="" {return empty,errors.New("owned database final helper runtime missing")}
 scannerState,helperState:=scanner.State.Terminated,helper.State.Terminated
 statuses,err:=json.Marshal(struct{Containers,Initializers []corev1.ContainerStatus}{pod.Status.ContainerStatuses,pod.Status.InitContainerStatuses})
 if err!=nil||len(statuses)>databaseScanMaximumAPIBytes {clear(statuses);return empty,errors.New("owned database runtime status bound invalid")}
 runtimeDigest:=digest.FromBytes(statuses).String();clear(statuses)
 helpers:=make([]string,len(pod.Status.InitContainerStatuses));for index,status:=range pod.Status.InitContainerStatuses {helpers[index]=status.ImageID}
 result:=databaseScanStatus{scannerImageID:scanner.ImageID,helperImageID:helper.ImageID,helperImageIDs:helpers,runtimeDigest:runtimeDigest,scannerStarted:scannerState.StartedAt.Time.UTC(),scannerFinished:scannerState.FinishedAt.Time.UTC(),helperFinished:helperState.FinishedAt.Time.UTC()}
 if !result.scannerFinished.After(result.scannerStarted)||result.helperFinished.Before(result.scannerFinished)||result.helperFinished.After(time.Now().UTC()) {return empty,errors.New("owned database runtime clock ordering invalid")}
 return result,nil
}
func databaseScanTerminatedCleanly(status corev1.ContainerStatus)bool {
 value:=status.State.Terminated
 return status.RestartCount==0&&status.ImageID!=""&&status.ContainerID!=""&&value!=nil&&value.ExitCode==0&&value.Signal==0&&value.Reason=="Completed"&&!value.StartedAt.IsZero()&&!value.FinishedAt.IsZero()&&value.FinishedAt.After(value.StartedAt.Time)&&status.LastTerminationState.Terminated==nil
}
func databaseScanGetBytes(ctx context.Context,requester databaseScanRequester,cluster uuid.UUID,path string,maximum int)([]byte,error) {
 if ctx==nil||requester==nil||maximum<=0||maximum>databaseScanMaximumReportBytes {return nil,errors.New("owned database scan read inputs invalid")}
 if err:=ctx.Err();err!=nil{return nil,err}
 bounded,cancel:=context.WithTimeout(ctx,15*time.Second);defer cancel()
 response,err:=requester.Do(bounded,cluster.String(),"GET",path,nil,nil)
 if bounded.Err()!=nil{return nil,bounded.Err()}
 if err==nil&&response!=nil&&response.StatusCode==410{return nil,errDatabaseScanGone}
 if err!=nil||response==nil||response.StatusCode!=200||len(response.Body)>base64.StdEncoding.EncodedLen(maximum) {return nil,errors.New("owned database scan API or log read unavailable")}
 payload,err:=base64.StdEncoding.Strict().DecodeString(response.Body)
 if err!=nil||len(payload)==0||len(payload)>maximum {clear(payload);return nil,errors.New("owned database scan API or log read invalid")}
 return payload,nil
}


func databaseScanJobSpecMatches(expected,actual *batchv1.Job)bool {
 if !databaseScanPodSpecMatches(expected.Spec.Template.Spec,actual.Spec.Template.Spec)||!reflect.DeepEqual(expected.Spec.Template.Annotations,actual.Spec.Template.Annotations) {return false}
 value:=actual.Spec.DeepCopy()
 // Normalize only exact documented API defaults of the canonical request.
 if expected.Spec.Parallelism==nil&&value.Parallelism!=nil&&*value.Parallelism==1 {value.Parallelism=nil}
 if expected.Spec.Completions==nil&&value.Completions!=nil&&*value.Completions==1 {value.Completions=nil}
 if expected.Spec.CompletionMode==nil&&value.CompletionMode!=nil&&*value.CompletionMode==batchv1.NonIndexedCompletion {value.CompletionMode=nil}
 if expected.Spec.Suspend==nil&&value.Suspend!=nil&&!*value.Suspend {value.Suspend=nil}
 if expected.Spec.ManualSelector==nil&&value.ManualSelector!=nil&&!*value.ManualSelector {value.ManualSelector=nil}
 if expected.Spec.PodReplacementPolicy==nil&&value.PodReplacementPolicy!=nil&&*value.PodReplacementPolicy==batchv1.TerminatingOrFailed {value.PodReplacementPolicy=nil}
 if expected.Spec.Selector==nil {
  if value.Selector==nil||len(value.Selector.MatchExpressions)!=0||len(value.Selector.MatchLabels)==0 {return false}
  for key,content:=range value.Selector.MatchLabels {if (key!="controller-uid"&&key!="batch.kubernetes.io/controller-uid")||content!=string(actual.UID) {return false}}
  value.Selector=nil
 }
 value.Template=expected.Spec.Template
 return reflect.DeepEqual(expected.Spec,*value)
}

func normalizeDatabasePodListItem(pod *corev1.Pod)error {
 if pod==nil||pod.Kind!=""&&pod.Kind!="Pod"||pod.APIVersion!=""&&pod.APIVersion!="v1" {return errors.New("owned database typed PodList item conflicts")}
 pod.Kind="Pod";pod.APIVersion="v1"
 return nil
}

func databaseScanResourceListsMatch(expected,actual corev1.ResourceList)bool {
 if len(expected)!=len(actual){return false}
 for key,wanted:=range expected {value,found:=actual[key];if !found||wanted.Cmp(value)!=0{return false}}
 return true
}
