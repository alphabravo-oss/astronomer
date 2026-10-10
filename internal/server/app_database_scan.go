package server

import (
 "context"
 "errors"
 "sort"
 "strings"

 corev1 "k8s.io/api/core/v1"

 "github.com/alphabravocompany/astronomer-go/internal/config"
 "github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
 "github.com/alphabravocompany/astronomer-go/internal/delivery/provider"
 "github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
 "github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
)

func (c *productionComposition) configureOwnedDatabaseScan(ctx context.Context,cfg *config.Config,original *provider.Provider,authority *distribution.DatabaseAuthority,proof *distribution.VerifiedDatabaseAuthorityBase)error {
 if err:=validateOwnedDatabaseScanSettings(cfg);err!=nil{return err}
 if !cfg.DeliveryDatabaseScanEnabled {
  if cfg.DeliveryDatabaseJobPullCredentialRef!="" {return errors.New("database Job pull requires explicit measured scanning")}
  return nil
 }
 if !cfg.DeliveryDatabaseUpdatesEnabled||c.requester==nil {return errors.New("measured database scanning requires owned update authority and current tunnel")}
 images,err:=authority.DatabaseScanImages(ctx,proof)
 if err!=nil{return err}
 material,err:=authority.CaptureJobMaterial(ctx,proof,cfg.ReleaseManifestPath,cfg.ReleaseMirrorMappingPath)
 if err!=nil{return err}
 var pull *resolver.DatabaseJobPullCredential
 retained:=false;defer func(){if !retained {material.Close();if pull!=nil {pull.Close()}}}()
 if cfg.DeliveryDatabaseJobPullCredentialRef!="" {
  hosts:=map[string]bool{}
  for _,image:=range []distribution.DatabaseScanImage{images.Helper,images.Scanner} {
   repository,_,found:=strings.Cut(image.Reference,"@");host,_,slash:=strings.Cut(repository,"/")
   if !found||!slash||host=="" {return errors.New("database Job selected registry scope unavailable")};hosts[host]=true
  }
  registries:=make([]string,0,len(hosts));for host:=range hosts {registries=append(registries,host)};sort.Strings(registries)
  pull,err=resolver.CaptureDatabaseJobPullCredential(ctx,cfg.DeliverySourceTrustDirectory,cfg.DeliveryDatabaseJobPullCredentialRef,registries)
  if err!=nil{return err}
 }
 environment,err:=databaseScanPreparationEnvironment(cfg)
 if err!=nil{return err}
 owner,err:=provider.NewDatabaseScanOwner(ctx,c.database.Pool(),original,authority,c.requester,cfg.DeliveryDatabaseUpdateRepository,environment,material,pull)
 if err!=nil{return err}
 c.databaseScanOwner=owner;c.databaseScanMaterial=material;c.databaseScanPull=pull;retained=true
 return nil
}
func databaseScanPreparationEnvironment(cfg *config.Config)([]corev1.EnvVar,error) {
 if cfg==nil||cfg.DistributionMirrorKeyRef!="mirror"||cfg.DeliveryDatabaseRegistryRootsRef!="registry" {return nil,errors.New("owned database preparation fixed references differ")}
 values:=map[string]string{
  "DELIVERY_ENABLED":"true","DELIVERY_DATABASE_UPDATES_ENABLED":"true","TMPDIR":"/tmp","HOME":"/tmp",
  "RELEASE_MANIFEST_PATH":databasejob.PrivateJobMaterialRoot+"/release.json",
  "DELIVERY_SOURCE_TRUST_DIRECTORY":databasejob.PrivateJobMaterialRoot+"/trust",
  "DISTRIBUTION_MIRROR_PLAN_PATH":databasejob.PrivateJobMaterialRoot+"/mirror-plan.json",
  "DISTRIBUTION_MIRROR_SIGNATURE_PATH":databasejob.PrivateJobMaterialRoot+"/mirror-signature.json",
  "DISTRIBUTION_MIRROR_KEY_REF":"mirror","DELIVERY_DATABASE_REGISTRY_ROOTS_REF":"registry",
  // Per-claim builder replaces this DATA with the exact canonical volume path.
  "DELIVERY_DATABASE_CACHE_DIRECTORY":databasejob.InputVolumeRoot+"/unselected",
  "DISTRIBUTION_DESTINATION_POLICY":cfg.DistributionDestinationPolicy,
  "DELIVERY_DATABASE_SCANNER_DIGEST":cfg.DeliveryDatabaseScannerDigest,
  "DELIVERY_DATABASE_SCANNER_VERSION":cfg.DeliveryDatabaseScannerVersion,
  "DELIVERY_DATABASE_UPDATE_REPOSITORY":cfg.DeliveryDatabaseUpdateRepository,
 }
 if cfg.ReleaseMirrorMappingPath!="" {values["RELEASE_MIRROR_MAPPING_PATH"]=databasejob.PrivateJobMaterialRoot+"/release-mapping.json"}
 read,err:=databaseReadRegistryOptions(cfg)
 if err!=nil{return nil,err}
 if read!=nil {
  if cfg.DeliveryDatabaseReadCredentialRef!="pull" {return nil,errors.New("owned database preparation pull reference differs")}
  values["DELIVERY_DATABASE_READ_CREDENTIAL_REF"]="pull";values["DELIVERY_DATABASE_READ_REGISTRY"]=cfg.DeliveryDatabaseReadRegistry
  values["DELIVERY_DATABASE_READ_REALM_URL"]=cfg.DeliveryDatabaseReadRealmURL;values["DELIVERY_DATABASE_READ_SERVICE"]=cfg.DeliveryDatabaseReadService;values["DELIVERY_DATABASE_READ_REPOSITORIES"]=cfg.DeliveryDatabaseReadRepositories
 }
 names:=make([]string,0,len(values));for name:=range values {names=append(names,name)};sort.Strings(names)
 result:=make([]corev1.EnvVar,0,len(names));for _,name:=range names {result=append(result,corev1.EnvVar{Name:name,Value:values[name]})}
 return result,nil
}

// Validate explicit feature relationships before any legacy loader branch or
// trust/DB constructor. A requested measured scan may never silently disappear.
func validateOwnedDatabaseScanSettings(cfg *config.Config)error {
 if cfg==nil{return errors.New("database scan configuration unavailable")}
 if cfg.DeliveryDatabaseScanEnabled&&(!cfg.DeliveryEnabled||!cfg.DeliveryDatabaseUpdatesEnabled) {return errors.New("measured database scanning requires enabled delivery and update authority")}
 if cfg.DeliveryDatabaseJobPullCredentialRef!=""&&!cfg.DeliveryDatabaseScanEnabled {return errors.New("database Job pull requires explicit measured scanning")}
 return nil
}
