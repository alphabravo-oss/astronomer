package delivery

import (
	"bytes"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
)

func TestOCITLSAndRegistryAuthenticationReferences(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		assignment := helmOCIAssignment()
		names := Names(assignment.ProjectID, assignment.DeploymentID)
		assignment.Source.CredentialSecret = names.AuthSecret
		assignment.Credential.Data["caFile"] = []byte("CA")
		assignment.Credential.Data["certFile"] = []byte("CLIENT")
		assignment.Credential.Data["keyFile"] = []byte("KEY")
		if mixed {
			assignment.Credential.Data["username"] = []byte("robot")
			assignment.Credential.Data["password"] = []byte("fixture")
		}
		materialized, err := BuildAssignment(assignment, testCapabilities(), ValidationPolicy{AllowPlatformScope: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, object := range materialized.Objects {
			if object.GetKind() == "Secret" && object.GetName() == names.AuthSecret {
				for key, want := range map[string]string{"ca.crt": "CA", "tls.crt": "CLIENT", "tls.key": "KEY"} {
					got, found, _ := unstructured.NestedFieldNoCopy(object.Object, "data", key)
					if !found || !bytes.Equal(got.([]byte), []byte(want)) {
						t.Fatal("TLS Secret key mismatch")
					}
				}
			}
			if object.GetKind() == "OCIRepository" {
				tlsName, _, _ := unstructured.NestedString(object.Object, "spec", "certSecretRef", "name")
				if tlsName != names.AuthSecret {
					t.Fatal("TLS reference absent")
				}
				authName, found, _ := unstructured.NestedString(object.Object, "spec", "secretRef", "name")
				if found != mixed || mixed && authName != names.AuthSecret {
					t.Fatal("registry auth reference mismatch")
				}
				if _, found, _ := unstructured.NestedFieldNoCopy(object.Object, "spec", "verify"); !found {
					t.Fatal("vendor trust removed")
				}
				if _, found, _ := unstructured.NestedFieldNoCopy(object.Object, "spec", "insecure"); found {
					t.Fatal("TLS bypass emitted")
				}
			}
		}
		if string(assignment.Credential.Data["caFile"]) != "CA" {
			t.Fatal("materialization mutated received credential")
		}
		delete(assignment.Credential.Data, "keyFile")
		if _, err := BuildAssignment(assignment, testCapabilities(), ValidationPolicy{AllowPlatformScope: true}); err == nil {
			t.Fatal("incomplete client TLS pair accepted")
		}
	}
}
