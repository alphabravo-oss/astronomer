package provider

import (
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "errors"
 "net/url"
 "strings"

 "github.com/google/uuid"
 corev1 "k8s.io/api/core/v1"
 "k8s.io/apimachinery/pkg/types"
 "k8s.io/apimachinery/pkg/util/validation"

 "github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
 "github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
 "github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

// Every check uses current tunnel reads, not helper annotations or stale report
// labels. The claim originates from the same checks at owned reservation time.
// A runtime image ID alone cannot establish the selected target or Pod spec.
func (owner *DatabaseScanOwner) recheckCurrentWorkload(ctx context.Context,claim sqlc.VulnerabilityDatabaseScanClaim,metadata resolver.WorkloadImageMetadata)error {
 if ctx==nil||owner==nil||owner.requester==nil||claim.WorkloadPodUid==uuid.Nil||claim.NodeUid==uuid.Nil||claim.ClusterID==uuid.Nil||claim.WorkloadNamespace==""||claim.WorkloadPodName==""||claim.NodeName==""||len(validation.IsDNS1123Label(claim.WorkloadNamespace))!=0||len(validation.IsDNS1123Subdomain(claim.WorkloadPodName))!=0||len(validation.IsDNS1123Subdomain(claim.NodeName))!=0 {return errors.New("owned database current workload inputs invalid")}
 path:="/api/v1/namespaces/"+url.PathEscape(claim.WorkloadNamespace)+"/pods/"+url.PathEscape(claim.WorkloadPodName)
 scoped,err:=newDatabaseScanScopedRequester(owner.requester,claim.ClusterID,[]string{path,"/api/v1/nodes/"+url.PathEscape(claim.NodeName)},nil)
 if err!=nil{return err}
 payload,err:=databaseScanGetBytes(ctx,scoped,claim.ClusterID,path,databaseScanMaximumAPIBytes)
 if err!=nil{return err};defer clear(payload)
 var pod corev1.Pod
 if jsoncontract.Decode(payload,&pod)!=nil {return errors.New("owned database current workload unavailable")}
 if err:=databaseCurrentWorkloadMatches(&pod,claim,metadata);err!=nil{return err}
 nodeBytes,err:=databaseScanGetBytes(ctx,scoped,claim.ClusterID,"/api/v1/nodes/"+url.PathEscape(claim.NodeName),databaseScanMaximumAPIBytes)
 if err!=nil{return err};defer clear(nodeBytes)
 var node corev1.Node
 if jsoncontract.Decode(nodeBytes,&node)!=nil||node.Kind!="Node"||node.APIVersion!="v1"||node.Name!=claim.NodeName||node.UID!=types.UID(claim.NodeUid.String())||node.DeletionTimestamp!=nil||node.Spec.Unschedulable||node.Status.NodeInfo.OperatingSystem!="linux"||node.Status.NodeInfo.Architecture!=claim.Architecture||node.Labels["kubernetes.io/os"]!="linux"||node.Labels["kubernetes.io/arch"]!=claim.Architecture {return errors.New("owned database current workload node differs")}
 ready:=false
 for _,condition:=range node.Status.Conditions {if condition.Type==corev1.NodeReady {ready=condition.Status==corev1.ConditionTrue}}
 if !ready {return errors.New("owned database current workload node not ready")}
 return ctx.Err()
}

func databaseCurrentWorkloadMatches(pod *corev1.Pod,claim sqlc.VulnerabilityDatabaseScanClaim,metadata resolver.WorkloadImageMetadata)error {
 if pod==nil||pod.Kind!="Pod"||pod.APIVersion!="v1"||pod.UID!=types.UID(claim.WorkloadPodUid.String())||pod.Name!=claim.WorkloadPodName||pod.Namespace!=claim.WorkloadNamespace||pod.DeletionTimestamp!=nil||pod.Spec.NodeName!=claim.NodeName||pod.Status.Phase!=corev1.PodRunning {return errors.New("owned database current workload UID or scheduling differs")}
 spec,err:=databaseWorkloadSpecDigest(pod.Spec)
 if err!=nil||spec!=claim.WorkloadSpecDigest {return errors.New("owned database current workload spec differs")}
 count,statusCount:=0,0
 for _,container:=range pod.Spec.Containers {if container.Name==claim.WorkloadContainerName {count++;if container.Image!=claim.WorkloadImage {return errors.New("owned database current workload configured image changed")}}}
 for _,status:=range pod.Status.ContainerStatuses {if status.Name==claim.WorkloadContainerName {statusCount++;if !status.Ready||status.State.Running==nil||status.ImageID!=claim.WorkloadImageID||!databaseRuntimeImageIDMatches(status.ImageID,claim.WorkloadRepository,metadata)||status.RestartCount!=claim.WorkloadRestartCount||status.ContainerID!=claim.WorkloadContainerID {return errors.New("owned database current workload runtime changed")}}}
 if count!=1||statusCount!=1 {return errors.New("owned database current workload container absent or duplicated")}
 // Runtime closure applies to this exact running container, never init or
 // ephemeral workloads selected by an untrusted log or matching image string.
 if strings.Contains(claim.WorkloadContainerID,"\n")||claim.WorkloadContainerID=="" {return errors.New("owned database current workload container identity invalid")}
 return nil
}

// This is a new workload comparison domain, not the canonical assignment or
// deployment compiler. The full spec is never stored/logged; only its hash is.
func databaseWorkloadSpecDigest(spec corev1.PodSpec)(string,error) {
 payload,err:=json.Marshal(spec)
 if err!=nil||len(payload)>databaseScanMaximumAPIBytes {return "",errors.New("owned database workload spec bound invalid")}
 defer clear(payload)
 hash:=sha256.New();_,_=hash.Write([]byte("astronomer-owned-workload-spec-v1\x00"));_,_=hash.Write(payload)
 return "sha256:"+hex.EncodeToString(hash.Sum(nil)),nil
}
