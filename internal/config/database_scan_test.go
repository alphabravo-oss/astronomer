package config

import "testing"

func TestDatabaseScanConfigurationRequiresExplicitEnablement(t *testing.T) {
 for _,enabled:=range []string{"true","false"} {
  t.Run(enabled,func(t *testing.T){
   databaseUpdateEnvironmentFixture(t,"true")
   t.Setenv("DELIVERY_DATABASE_SCAN_ENABLED",enabled)
   t.Setenv("DELIVERY_DATABASE_JOB_PULL_CREDENTIAL_REF","kubelet")
   cfg,err:=Load()
   if err!=nil||cfg.DeliveryDatabaseScanEnabled!=(enabled=="true")||cfg.DeliveryDatabaseJobPullCredentialRef!="kubelet" {t.Fatal("explicit typed scan configuration did not bind")}
  })
 }
}
func TestDatabaseScanCredentialDataCannotEnableDefaultScanning(t *testing.T) {
 databaseUpdateEnvironmentFixture(t,"true")
 t.Setenv("DELIVERY_DATABASE_SCAN_ENABLED","")
 t.Setenv("DELIVERY_DATABASE_JOB_PULL_CREDENTIAL_REF","kubelet")
 cfg,err:=Load()
 if err!=nil||cfg.DeliveryDatabaseScanEnabled||cfg.DeliveryDatabaseJobPullCredentialRef!="kubelet" {t.Fatal("credential DATA implicitly enabled measured Jobs")}
 t.Setenv("DELIVERY_DATABASE_JOB_PULL_CREDENTIAL_REF","")
 cfg,err=Load()
 if err!=nil||cfg.DeliveryDatabaseScanEnabled||cfg.DeliveryDatabaseJobPullCredentialRef!="" {t.Fatal("ordinary default acquired Job ownership")}
}
