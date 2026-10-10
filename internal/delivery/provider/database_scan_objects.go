package provider

import (
 "context"
 "errors"
 "net/url"

 "github.com/google/uuid"
 batchv1 "k8s.io/api/batch/v1"
 corev1 "k8s.io/api/core/v1"

 "github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
 "github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
)

func (owner *DatabaseScanOwner) buildClaimJobs(ctx context.Context,claim sqlc.VulnerabilityDatabaseScanClaim)(databasejob.ScanJobs,error) {
 if owner==nil||owner.material==nil||claim.ID==uuid.Nil {return databasejob.ScanJobs{},errors.New("owned database claim builder unavailable")}
 if err:=owner.material.ValidateAuthority(ctx,owner.projection.authority);err!=nil{return databasejob.ScanJobs{},err}
 data,privatePull,mappedRelease,err:=owner.material.Data(ctx)
 if err!=nil{return databasejob.ScanJobs{},err}
 for _,member:=range data {clear(member)}
 environment:=make([]corev1.EnvVar,len(owner.preparationEnvironment))
 copy(environment,owner.preparationEnvironment)
 for index:=range environment {if environment[index].Name=="DELIVERY_DATABASE_CACHE_DIRECTORY" {environment[index].Value=databasejob.InputVolumeRoot+"/input-"+claim.ID.String()+"/staging"}}
 return databasejob.BuildScanJobs(ctx,owner.projection.authority,owner.projection.proof,databasejob.JobRequest{ClaimID:claim.ID,FeedDigest:claim.FeedDigest,WorkloadRepository:claim.WorkloadRepository,WorkloadDigest:claim.WorkloadManifestDigest,Architecture:claim.Architecture,NodeName:claim.NodeName,PreparationEnvironment:environment,PrivatePull:privatePull,MappedRelease:mappedRelease,ImagePullSecret:owner.claimPullSecretName(claim.ID)})
}

func (owner *DatabaseScanOwner) claimReadRequester(claim sqlc.VulnerabilityDatabaseScanClaim,job *batchv1.Job)(*databaseScanScopedRequester,error) {
 if owner==nil||job==nil||!claim.ScanJobUid.Valid||claim.ScanPodName=="" {return nil,errors.New("owned database current scan read scope unavailable")}
 selector:="batch.kubernetes.io/controller-uid="+uuid.UUID(claim.ScanJobUid.Bytes).String()
 reads:=[]string{
  "/apis/batch/v1/namespaces/"+databasejob.ScanNamespace+"/jobs/"+url.PathEscape(job.Name),
  "/api/v1/namespaces/"+databasejob.ScanNamespace+"/pods?labelSelector="+url.QueryEscape(selector)+"&limit=2",
  databaseScanPodPath(claim.ScanPodName)+"/log?container=observe&timestamps=true&limitBytes=33025",
  databaseScanPodPath(claim.ScanPodName)+"/log?container=scanner&timestamps=false&limitBytes=8388609",
 }
 return newDatabaseScanScopedRequester(owner.requester,claim.ClusterID,reads,nil)
}

func (owner *DatabaseScanOwner) claimPullSecretName(claim uuid.UUID)string {
 if owner!=nil&&owner.kubeletPull!=nil{return "database-scan-"+claim.String()+"-image-pull"};return ""
}
func (owner *DatabaseScanOwner) revalidateScanMaterial(ctx context.Context)error {
 if owner==nil||owner.material==nil{return errors.New("owned database scan material unavailable")}
 if err:=owner.material.Revalidate(ctx);err!=nil{return err}
 if owner.kubeletPull!=nil {return owner.kubeletPull.Revalidate(ctx)}
 return ctx.Err()
}
