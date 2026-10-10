package provider

import (
 "context"
 "errors"
 "net/url"
 "strings"

 "github.com/google/uuid"
 corev1 "k8s.io/api/core/v1"
 "oras.land/oras-go/v2/registry/remote"

 "github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
 "github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
 "github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
)

// Bounded cluster workload retrieval is DATA only. Complete scanner target,
// current source and current accepted update are checked by the transaction.
// No query filters invalid materialized sources out of required validation.
func (owner *DatabaseScanOwner) discoverCluster(ctx context.Context,cluster uuid.UUID)error {
 digest,err:=sqlc.New(owner.pool).GetOwnedDatabaseDiscoveryFeed(ctx,cluster)
 if err!=nil{return err}
 token,err:=sqlc.New(owner.pool).GetOwnedDatabasePodDiscoveryCursor(ctx,cluster)
 if err!=nil{return err}
 path:="/api/v1/pods?limit=64";if token!="" {path+="&continue="+url.QueryEscape(token)}
 scoped,err:=newDatabaseScanScopedRequester(owner.requester,cluster,[]string{path},nil)
 if err!=nil{return err}
 payload,err:=databaseScanGetBytes(ctx,scoped,cluster,path,databaseScanMaximumAPIBytes)
 if err!=nil {
  // Only an actual bounded 410 on this continued-list request can reset a
  // snapshot. It does not grant complete discovery or hide the error.
  if token!=""&&errors.Is(err,errDatabaseScanGone) {
   if reset:=sqlc.New(owner.pool).PutOwnedDatabasePodDiscoveryCursor(ctx,sqlc.PutOwnedDatabasePodDiscoveryCursorParams{ClusterID:cluster,Continuation:""});reset!=nil{return reset}
  }
  return err
 }
 var pods corev1.PodList
 decodeErr:=jsoncontract.Decode(payload,&pods);clear(payload)
 if decodeErr!=nil||pods.Kind!="PodList"||pods.APIVersion!="v1"||len(pods.Items)>64||len(pods.Continue)>4096||token!=""&&pods.Continue==token {return errors.New("owned database workload discovery response invalid")}
 var problems []error
 for index:=range pods.Items {
  if err:=ctx.Err();err!=nil{return err}
  pod:=&pods.Items[index]
  if err:=normalizeDatabasePodListItem(pod);err!=nil {problems=append(problems,databaseScanUnknownProgress(err,"owned database workload item unknown"));continue}
  if pod.Namespace==databasejob.ScanNamespace||pod.DeletionTimestamp!=nil||pod.Status.Phase!=corev1.PodRunning {continue}
  for _,container:=range pod.Spec.Containers {
   // Unsupported actual identities never acquire clean posture; failures are
   // aggregated so one unsupported workload cannot starve later page items.
   if err:=owner.discoverContainer(ctx,cluster,digest,pod,container);err!=nil {problems=append(problems,databaseScanUnknownProgress(err,"owned database workload item unknown"))}
   if err:=ctx.Err();err!=nil{return err}
  }
 }
 // One bounded page per cluster makes other clusters fair. Continue is opaque
 // retrieval DATA and is never logged or allowed to control a network origin.
 if err:=sqlc.New(owner.pool).PutOwnedDatabasePodDiscoveryCursor(ctx,sqlc.PutOwnedDatabasePodDiscoveryCursorParams{ClusterID:cluster,Continuation:pods.Continue});err!=nil{return err}
 if pods.Continue!="" {problems=append(problems,errors.New("owned database workload discovery incomplete"))}
 return errors.Join(problems...)
}
func (owner *DatabaseScanOwner) discoverContainer(ctx context.Context,cluster uuid.UUID,feed string,pod *corev1.Pod,container corev1.Container)error {
 value,supported,err:=databaseScanWorkloadCandidate(cluster,pod,container)
 if err!=nil||!supported{return err}
 value.FeedDigest=feed
 node,err:=owner.readCandidateNode(ctx,cluster,value.NodeName)
 if err!=nil{return err}
 value.NodeUid=uuid.MustParse(string(node.UID));value.Architecture=node.Status.NodeInfo.Architecture
 metadata,err:=owner.projection.authority.ReadWorkloadImageMetadata(ctx,owner.projection.proof,value.WorkloadRepository,value.WorkloadManifestDigest,value.Architecture)
 if err!=nil{return err}
 defer clear(metadata.Manifest);defer clear(metadata.Config)
 _,err=owner.reserveWorkload(ctx,value,metadata)
 return err
}

func databaseScanWorkloadCandidate(cluster uuid.UUID,pod *corev1.Pod,container corev1.Container)(sqlc.VulnerabilityDatabaseScanClaim,bool,error) {
 value:=sqlc.VulnerabilityDatabaseScanClaim{}
 if pod==nil||pod.Kind!="Pod"||pod.APIVersion!="v1"||pod.DeletionTimestamp!=nil||pod.Status.Phase!=corev1.PodRunning||cluster==uuid.Nil||!validDatabaseScanUID(pod.UID)||pod.Namespace==""||pod.Name==""||pod.Spec.NodeName=="" {return value,false,errors.New("owned database workload identity invalid")}
 var selected *corev1.ContainerStatus
 for index:=range pod.Status.ContainerStatuses {if pod.Status.ContainerStatuses[index].Name==container.Name {if selected!=nil{return value,false,errors.New("owned database workload runtime duplicated")};selected=&pod.Status.ContainerStatuses[index]}}
 if selected==nil||!selected.Ready||selected.State.Running==nil||selected.ContainerID=="" {return value,false,nil}
 actual:=strings.TrimPrefix(selected.ImageID,"docker-pullable://")
 repository,digest,found:=strings.Cut(actual,"@")
 if !found||!validDatabaseScanDigest(digest)||strings.Contains(repository,"://") {return value,false,nil}
 parsed,parseErr:=remote.NewRepository(repository)
 if parseErr!=nil||parsed.Reference.Reference!=""||len(repository)>512 {return value,false,nil}
 spec,err:=databaseWorkloadSpecDigest(pod.Spec)
 if err!=nil{return value,false,err}
 value.ClusterID=cluster;value.WorkloadPodUid=uuid.MustParse(string(pod.UID));value.WorkloadNamespace=pod.Namespace;value.WorkloadPodName=pod.Name;value.WorkloadContainerName=container.Name
 value.WorkloadRepository=repository;value.WorkloadManifestDigest=digest;value.WorkloadImage=container.Image;value.WorkloadSpecDigest=spec;value.WorkloadRestartCount=selected.RestartCount;value.WorkloadContainerID=selected.ContainerID;value.WorkloadImageID=selected.ImageID;value.NodeName=pod.Spec.NodeName
 return value,true,nil
}
func (owner *DatabaseScanOwner) readCandidateNode(ctx context.Context,cluster uuid.UUID,name string)(corev1.Node,error) {
 var node corev1.Node
 path:="/api/v1/nodes/"+url.PathEscape(name)
 scoped,err:=newDatabaseScanScopedRequester(owner.requester,cluster,[]string{path},nil)
 if err!=nil{return node,err}
 payload,err:=databaseScanGetBytes(ctx,scoped,cluster,path,databaseScanMaximumAPIBytes)
 if err!=nil{return node,err};defer clear(payload)
 if jsoncontract.Decode(payload,&node)!=nil||node.Kind!="Node"||node.APIVersion!="v1"||node.Name!=name||!validDatabaseScanUID(node.UID)||node.DeletionTimestamp!=nil||node.Spec.Unschedulable||node.Status.NodeInfo.OperatingSystem!="linux"||(node.Status.NodeInfo.Architecture!="amd64"&&node.Status.NodeInfo.Architecture!="arm64")||node.Labels["kubernetes.io/os"]!="linux"||node.Labels["kubernetes.io/arch"]!=node.Status.NodeInfo.Architecture {return corev1.Node{},errors.New("owned database workload node unsupported")}
 ready:=false;for _,condition:=range node.Status.Conditions {if condition.Type==corev1.NodeReady {ready=condition.Status==corev1.ConditionTrue}}
 if !ready{return corev1.Node{},errors.New("owned database workload node unavailable")}
 return node,ctx.Err()
}
