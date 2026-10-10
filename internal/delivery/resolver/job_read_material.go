package resolver

import (
 "context"
 "crypto/sha256"
 "errors"
)

// JobReadCredentialMaterial is used only by an owned preparation Secret adapter.
// It never returns a push credential or inherits ambient registry credentials.
// The original captured pull scope/material must still be current; the caller
// must clear the returned bytes and revalidate before actual Secret use/commit.
func (transport *DatabaseRegistryTransport) JobReadCredentialMaterial(ctx context.Context)([]byte,error) {
 if transport==nil||ctx==nil {return nil,errors.New("owned Job read material unavailable")}
 if err:=transport.Revalidate(ctx);err!=nil{return nil,err}
 transport.mutex.RLock()
 defer transport.mutex.RUnlock()
 if transport.closed {return nil,errors.New("owned Job read material unavailable")}
 if transport.readOptions==nil {return nil,nil}
 if transport.readCredential==nil||transport.readOptions.CredentialRef!="pull" {return nil,errors.New("owned Job fixed pull reference differs")}
 material,_,err:=transport.mounted.readTrustKeyWithHook(ctx,transport.readOptions.CredentialRef,".json",nil)
 if err!=nil{return nil,databaseAuthenticationError(ctx,err)}
 if len(material)==0||len(material)>maxDatabaseReadCredentialBytes||sha256.Sum256(material)!=transport.readCredential.material {clearBytes(material);return nil,errors.New("owned Job mounted pull material changed")}
 if err:=transport.revalidateReadCredential(ctx);err!=nil {clearBytes(material);return nil,err}
 if err:=ctx.Err();err!=nil {clearBytes(material);return nil,err}
 return material,nil
}
