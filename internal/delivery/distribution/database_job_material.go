package distribution

import (
 "bytes"
 "context"
 "errors"
 "fmt"
 "io"
 "sync"

 digest "github.com/opencontainers/go-digest"

 "github.com/alphabravocompany/astronomer-go/internal/delivery/model"
 "github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// DatabaseJobMaterial is a private constructor-captured preparation Secret
// owner, not a signing key/approval/runtime or scan-use proof. Every export
// revalidates the ORIGINAL mounted roots, operator key, pull material, public
// release/mapping origins and the SAME canonical opaque authority.
type DatabaseJobMaterial struct {
 mutex *sync.Mutex
 authority *DatabaseAuthority
 proof *VerifiedDatabaseAuthorityBase
 releasePath,mappingPath string
 releaseHash,mappingHash string
 data map[string][]byte
 privatePull bool
 closed bool
}
func (DatabaseJobMaterial) Format(state fmt.State,_ rune) {_,_=io.WriteString(state,"<private owned Job preparation material>")}

// This optional process profile intentionally uses the fixed reviewed projected
// member names. It does not silently change signing KeyRef identity or use a
// publisher push credential. Ordinary server constructors remain unchanged.
func (authority *DatabaseAuthority) CaptureJobMaterial(ctx context.Context,proof *VerifiedDatabaseAuthorityBase,releasePath,mappingPath string)(*DatabaseJobMaterial,error) {
 if ctx==nil||authority==nil||proof==nil||proof.owner!=authority||releasePath=="" {return nil,errors.New("owned Job material constructor unavailable")}
 authority.mutex.RLock();defer authority.mutex.RUnlock()
 if authority.closed||authority.registry==nil||proof.fingerprint!=authority.physicalFingerprint()||authority.loader.options.Policy.Provider!=model.SignatureCosignKeyless {return nil,errors.New("owned Job material profile or authority differs")}
 if err:=authority.revalidatePhysicalTrust(ctx);err!=nil{return nil,err}
 plan,err:=authority.loader.options.MirrorPlan.immutable()
 if err!=nil||authority.mirrorTrust==nil||plan.verified.policy.KeyRef!="mirror" {return nil,errors.New("owned Job fixed operator mirror reference differs")}
 captured:=&DatabaseJobMaterial{mutex:&sync.Mutex{},authority:authority,proof:proof,releasePath:releasePath,mappingPath:mappingPath,data:map[string][]byte{}}
 retained:=false;defer func(){if !retained {captured.Close()}}()
 release,err:=resolver.ReadMountedJobMetadata(ctx,releasePath)
 if err!=nil{return nil,err}
 captured.data["release.json"]=release
 captured.releaseHash=digest.FromBytes(release).String()
 if captured.releaseHash!=plan.ReleaseManifestDigest {return nil,errors.New("owned Job release differs from authenticated operator endorsement")}
 captured.data["mirror-plan.json"]=bytes.Clone(plan.verified.payload)
 captured.data["mirror-signature.json"]=bytes.Clone(plan.verified.signature)
 captured.data["trusted_root.json"],err=authority.trust.PublicJobMaterial(ctx)
 if err!=nil{return nil,err}
 captured.data["mirror.pub"],err=authority.mirrorTrust.PublicJobMaterial(ctx)
 if err!=nil{return nil,err}
 captured.data["registry.pem"],err=authority.registry.CertificateAuthorities(ctx)
 if err!=nil{return nil,err}
 credential,err:=authority.registry.JobReadCredentialMaterial(ctx)
 if err!=nil{return nil,err}
 if len(credential)>0 {captured.privatePull=true;captured.data["pull.json"]=credential}
 if mappingPath!="" {
  captured.data["release-mapping.json"],err=resolver.ReadMountedJobMetadata(ctx,mappingPath)
  if err!=nil{return nil,err}
  captured.mappingHash=digest.FromBytes(captured.data["release-mapping.json"]).String()
 }
 if err:=authority.revalidatePhysicalTrust(ctx);err!=nil{return nil,err}
 if err:=captured.revalidatePublicOrigins(ctx);err!=nil{return nil,err}
 retained=true
 return captured,nil
}

func (captured *DatabaseJobMaterial) Revalidate(ctx context.Context)error {
 if captured==nil||ctx==nil||captured.mutex==nil {return errors.New("owned Job preparation material unavailable")}
 captured.mutex.Lock();defer captured.mutex.Unlock()
 if captured.closed {return errors.New("owned Job preparation material unavailable")}
 if err:=captured.authority.Revalidate(ctx,captured.proof);err!=nil{return err}
 return captured.revalidatePublicOrigins(ctx)
}
func (captured *DatabaseJobMaterial) revalidatePublicOrigins(ctx context.Context)error {
 for _,origin:=range []struct{path,hash string}{{captured.releasePath,captured.releaseHash},{captured.mappingPath,captured.mappingHash}} {
  if origin.path=="" {continue}
  hash,err:=resolver.FingerprintMountedPublicMetadata(ctx,origin.path)
  if err!=nil{return err}
  if hash!=origin.hash {return errors.New("owned Job public release or mapping source changed")}
 }
 return ctx.Err()
}
func (captured *DatabaseJobMaterial) Data(ctx context.Context)(map[string][]byte,bool,bool,error) {
 if captured==nil||ctx==nil||captured.mutex==nil {return nil,false,false,errors.New("owned Job preparation material unavailable")}
 if err:=captured.Revalidate(ctx);err!=nil{return nil,false,false,err}
 captured.mutex.Lock();defer captured.mutex.Unlock()
 if captured.closed {return nil,false,false,errors.New("owned Job preparation material unavailable")}
 result:=make(map[string][]byte,len(captured.data))
 for name,material:=range captured.data {result[name]=bytes.Clone(material)}
 return result,captured.privatePull,captured.mappingPath!="",nil
}
func (captured *DatabaseJobMaterial) Close() {
 if captured==nil||captured.mutex==nil{return}
 captured.mutex.Lock();defer captured.mutex.Unlock()
 if captured.closed{return};captured.closed=true
 for name,material:=range captured.data {clear(material);delete(captured.data,name)}
}

// Validates private constructor ownership, not caller-provided fingerprints.
func (captured *DatabaseJobMaterial) ValidateAuthority(ctx context.Context,authority *DatabaseAuthority)error {
 if captured==nil||authority==nil||captured.authority!=authority {return errors.New("owned Job material belongs to another authority")}
 return captured.Revalidate(ctx)
}
