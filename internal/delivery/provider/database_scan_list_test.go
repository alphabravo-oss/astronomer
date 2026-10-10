package provider

import (
 "encoding/json"
 "testing"

 corev1 "k8s.io/api/core/v1"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/runtime"
 "k8s.io/apimachinery/pkg/runtime/serializer"
)

// Actual selected Kubernetes serializer, not kubectl's printer. This test is
// a codec control; it does not stand in for the owned API default round trip.
func TestOwnedDatabasePodListSelectedCodecPermitsAbsentItemTypeMetaOnly(t *testing.T) {
 scheme:=runtime.NewScheme()
 if err:=corev1.AddToScheme(scheme);err!=nil{t.Fatal(err)}
 codec:=serializer.NewCodecFactory(scheme).LegacyCodec(corev1.SchemeGroupVersion)
 encoded,err:=runtime.Encode(codec,&corev1.PodList{Items:[]corev1.Pod{{ObjectMeta:metav1.ObjectMeta{Name:"actual-list-item"}}}})
 if err!=nil {t.Fatal(err)}
 var list corev1.PodList
 if err:=json.Unmarshal(encoded,&list);err!=nil||list.Kind!="PodList"||list.APIVersion!="v1"||len(list.Items)!=1 {t.Fatal("selected serializer fixture did not produce typed PodList")}
 if list.Items[0].Kind!=""||list.Items[0].APIVersion!="" {t.Fatal("selected serializer changed nested TypeMeta; investigate raw API shape")}
 if err:=normalizeDatabasePodListItem(&list.Items[0]);err!=nil||list.Items[0].Kind!="Pod"||list.Items[0].APIVersion!="v1" {t.Fatal("validated typed list item could not be normalized")}
 for _,meta:=range []metav1.TypeMeta{{Kind:"Secret"},{APIVersion:"apps/v1"},{Kind:"Pod",APIVersion:"apps/v1"}} {
  item:=corev1.Pod{TypeMeta:meta}
  if err:=normalizeDatabasePodListItem(&item);err==nil {t.Fatal("conflicting provided item TypeMeta accepted")}
 }
 // Single-object readback still checks nonempty v1/Pod in its own route.
}
func TestOwnedDatabaseObservationAcceptsRawListItemsWithoutWeakeningConflicts(t *testing.T) {
 request,peer:=databaseScanReadFixture(t)
 peer.pod.TypeMeta=metav1.TypeMeta{}
 observation,err:=observeDatabaseScan(t.Context(),peer,request)
 if err!=nil{t.Fatal("valid typed outer list with absent item TypeMeta rejected")};observation.close()
 request,peer=databaseScanReadFixture(t);peer.pod.Kind="Secret"
 observation,err=observeDatabaseScan(t.Context(),peer,request)
 if err==nil {observation.close();t.Fatal("conflicting typed outer-list item accepted")}
}
