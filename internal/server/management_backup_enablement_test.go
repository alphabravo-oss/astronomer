package server

import "testing"

type managementBackupStartupStub bool

func (ready managementBackupStartupStub) ManagementBackupReady() bool { return bool(ready) }

func TestValidateManagementBackupStartup(t *testing.T) {
	if err := validateManagementBackupStartup(false, nil); err != nil {
		t.Fatalf("disabled management backup failed startup: %v", err)
	}
	if err := validateManagementBackupStartup(true, managementBackupStartupStub(false)); err == nil {
		t.Fatal("enabled management backup accepted an unready executor")
	}
	if err := validateManagementBackupStartup(true, managementBackupStartupStub(true)); err != nil {
		t.Fatalf("enabled management backup rejected a ready executor: %v", err)
	}
}
