package provider

import (
 "strings"
 "testing"

 "github.com/google/uuid"
 digest "github.com/opencontainers/go-digest"
 ocispec "github.com/opencontainers/image-spec/specs-go/v1"
 corev1 "k8s.io/api/core/v1"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/types"

 "github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)
func TestOwnedWorkloadSelectionUsesRuntimeDigestAndPreservesMutableConfiguredData(t *testing.T) {
 pod:=&corev1.Pod{TypeMeta:metav1.TypeMeta{APIVersion:"v1",Kind:"Pod"},ObjectMeta:metav1.ObjectMeta{Name:"actual-workload",Namespace:"tenant",UID:types.UID(uuid.NewString())},Spec:corev1.PodSpec{NodeName:"actual-node",Containers:[]corev1.Container{{Name:"app",Image:"registry.example.test/app:mutable"}}},Status:corev1.PodStatus{Phase:corev1.PodRunning,ContainerStatuses:[]corev1.ContainerStatus{{Name:"app",Ready:true,ImageID:"docker-pullable://registry.example.test/app@sha256:"+strings.Repeat("a",64),ContainerID:"containerd://actual-id",State:corev1.ContainerState{Running:&corev1.ContainerStateRunning{}}}}}}
 selected,supported,err:=databaseScanWorkloadCandidate(uuid.New(),pod,pod.Spec.Containers[0])
 if err!=nil||!supported||selected.WorkloadManifestDigest!="sha256:"+strings.Repeat("a",64)||selected.WorkloadImage!=pod.Spec.Containers[0].Image||selected.WorkloadPodUid.String()!=string(pod.UID) {t.Fatal("runtime digest was reconstructed from mutable desired locator")}
 for _,identity:=range []string{"containerd://sha256:"+strings.Repeat("b",64),"registry.example.test/app:mutable","https://registry.example.test/app@sha256:"+strings.Repeat("a",64),"registry.example.test/app@sha256:bad"} {
  pod.Status.ContainerStatuses[0].ImageID=identity
  if _,supported,err:=databaseScanWorkloadCandidate(uuid.New(),pod,pod.Spec.Containers[0]);err!=nil||supported {t.Fatal("unsupported runtime identity must remain unknown, never guessed")}
 }
}
func TestOwnedWorkloadCurrentRecheckRejectsIdentitySpecAndRuntimeMutation(t *testing.T) {
 pod:=&corev1.Pod{TypeMeta:metav1.TypeMeta{APIVersion:"v1",Kind:"Pod"},ObjectMeta:metav1.ObjectMeta{Name:"actual-workload",Namespace:"tenant",UID:types.UID(uuid.NewString())},Spec:corev1.PodSpec{NodeName:"actual-node",Containers:[]corev1.Container{{Name:"app",Image:"registry.example.test/app:mutable"}}},Status:corev1.PodStatus{Phase:corev1.PodRunning,ContainerStatuses:[]corev1.ContainerStatus{{Name:"app",Ready:true,ImageID:"registry.example.test/app@sha256:"+strings.Repeat("a",64),ContainerID:"containerd://actual-id",State:corev1.ContainerState{Running:&corev1.ContainerStateRunning{}}}}}}
 value,supported,err:=databaseScanWorkloadCandidate(uuid.New(),pod,pod.Spec.Containers[0]);if err!=nil||!supported {t.Fatal("invalid fixture")}
 metadata:=resolver.WorkloadImageMetadata{ManifestDescriptor:ocispec.Descriptor{Digest:digest.Digest(value.WorkloadManifestDigest)},ConfigDescriptor:ocispec.Descriptor{Digest:digest.Digest("sha256:"+strings.Repeat("b",64))}}
 if err:=databaseCurrentWorkloadMatches(pod,value,metadata);err!=nil {t.Fatal(err)}
 for _,test:=range []struct{name string;change func(*corev1.Pod)}{
  {"UID",func(p *corev1.Pod){p.UID=types.UID(uuid.NewString())}},
  {"namespace",func(p *corev1.Pod){p.Namespace="foreign"}},
  {"node",func(p *corev1.Pod){p.Spec.NodeName="foreign-node"}},
  {"configured image",func(p *corev1.Pod){p.Spec.Containers[0].Image="changed:mutable"}},
  {"private spec change",func(p *corev1.Pod){p.Spec.Containers[0].Env=[]corev1.EnvVar{{Name:"private",Value:"changed"}}}},
  {"runtime imageID",func(p *corev1.Pod){p.Status.ContainerStatuses[0].ImageID="registry.example.test/app@sha256:"+strings.Repeat("c",64)}},
  {"container restart",func(p *corev1.Pod){p.Status.ContainerStatuses[0].RestartCount++}},
  {"container ID",func(p *corev1.Pod){p.Status.ContainerStatuses[0].ContainerID="containerd://replacement"}},
  {"not ready",func(p *corev1.Pod){p.Status.ContainerStatuses[0].Ready=false}},
  {"status duplicate",func(p *corev1.Pod){p.Status.ContainerStatuses=append(p.Status.ContainerStatuses,p.Status.ContainerStatuses[0])}},
 } {t.Run(test.name,func(t *testing.T){copy:=pod.DeepCopy();test.change(copy);if err:=databaseCurrentWorkloadMatches(copy,value,metadata);err==nil {t.Fatal("changed current workload retained witness authority")}})}
}
