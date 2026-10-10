package observability

import (
	"fmt"
	"testing"
	"time"
)

func TestRegistrationMetricSnapshotResyncsCommittedReadyAfterDroppedFlush(t *testing.T) {
	isolateRegistrationMetricOwners(t)
	hook := NewRegistrationMetricsHook()
	committed := hook.CapturePhaseTransition("new-row", "", "ready")
	// The transaction's newly inserted row is still invisible to an independent
	// older RR observation. It retires the unpublished logical capture.
	PublishRegistrationMetricSnapshot(nil, RegistrationMetricsWatermark(), time.Now())
	committed()
	if labels := baselineMetricLabels(t, "astronomer_cluster_registration_phase"); len(labels) != 0 {
		t.Fatal("retired capture resurrected a phase")
	}
	// No subsequent writer transition is required: the next complete database
	// snapshot contains the committed terminal ready row and restores its gauge.
	if !PublishRegistrationMetricSnapshot([]RegistrationMetricSnapshotRow{{"new-row", "ready"}}, RegistrationMetricsWatermark(), time.Now()) {
		t.Fatal("complete next snapshot refused ready row")
	}
	labels := baselineMetricLabels(t, "astronomer_cluster_registration_phase")
	if len(labels) != 1 || labels[0]["phase"] != "ready" {
		t.Fatalf("committed ready phase remains absent: %v", labels)
	}
	committed()
	if labels := baselineMetricLabels(t, "astronomer_cluster_registration_phase"); len(labels) != 1 || labels[0]["phase"] != "ready" {
		t.Fatal("old retired capture changed resynchronized phase")
	}
}

func TestRegistrationMetricSnapshotFencesStaleRowsAndRecreatedOwner(t *testing.T) {
	isolateRegistrationMetricOwners(t)
	hook := NewRegistrationMetricsHook()
	hook.RecordPhaseTransition("owned", "", "connected")
	late := hook.CapturePhaseTransition("owned", "connected", "failed")
	oldSnapshot := RegistrationMetricsWatermark()
	// Newer complete absence confirms committed tombstone/deletion. An older
	// ready observation cannot recreate it after that authoritative retirement.
	PublishRegistrationMetricSnapshot(nil, RegistrationMetricsWatermark(), time.Now())
	if PublishRegistrationMetricSnapshot([]RegistrationMetricSnapshotRow{{"owned", "ready"}}, oldSnapshot, time.Now()) {
		t.Fatal("old database rows republished after newer committed absence")
	}
	late()
	if labels := baselineMetricLabels(t, "astronomer_cluster_registration_phase"); len(labels) != 0 {
		t.Fatal("deleted owner resurrected")
	}
	PublishRegistrationMetricSnapshot([]RegistrationMetricSnapshotRow{{"owned", "created"}}, RegistrationMetricsWatermark(), time.Now())
	late()
	labels := baselineMetricLabels(t, "astronomer_cluster_registration_phase")
	if len(labels) != 1 || labels[0]["phase"] != "created" {
		t.Fatalf("stale callback changed recreated owner: %v", labels)
	}
	beforeRead := RegistrationMetricsWatermark()
	hook.RecordPhaseTransition("owned", "created", "connected")
	PublishRegistrationMetricSnapshot([]RegistrationMetricSnapshotRow{{"owned", "created"}}, beforeRead, time.Now())
	labels = baselineMetricLabels(t, "astronomer_cluster_registration_phase")
	if len(labels) != 1 || labels[0]["phase"] != "connected" {
		t.Fatal("older row overwrote admission after the read watermark")
	}
}

func TestRegistrationMetricSnapshotAtomicCapAndInvalidObservations(t *testing.T) {
	isolateRegistrationMetricOwners(t)
	hook := NewRegistrationMetricsHook()
	hook.RecordPhaseTransition("current", "", "connected")
	initial := time.Unix(100, 0)
	PublishRegistrationMetricSnapshot([]RegistrationMetricSnapshotRow{{"current", "connected"}}, RegistrationMetricsWatermark(), initial)
	watermark := RegistrationMetricsWatermark()
	// Fresh captures absent from the in-flight snapshot consume the registry's
	// remaining capacity, so a complete full-size result would overflow.
	hook.RecordPhaseTransition("fresh-extra", "", "created")
	registrationMetricOwners.Lock()
	foreign := 0
	for key := range registrationMetricOwners.rows {
		if key.instance != InstanceID() {
			foreign++
		}
	}
	registrationMetricOwners.Unlock()
	rows := make([]RegistrationMetricSnapshotRow, MetricOwnerMax-foreign)
	for i := range rows {
		rows[i] = RegistrationMetricSnapshotRow{fmt.Sprintf("new-%d", i), "ready"}
	}
	if PublishRegistrationMetricSnapshot(rows, watermark, time.Unix(200, 0)) {
		t.Fatal("publication exceeded map admission ceiling")
	}
	for _, invalid := range [][]RegistrationMetricSnapshotRow{make([]RegistrationMetricSnapshotRow, MetricOwnerMax+1), {{"", "ready"}}, {{"duplicate", "ready"}, {"duplicate", "ready"}}} {
		if PublishRegistrationMetricSnapshot(invalid, RegistrationMetricsWatermark(), time.Unix(300, 0)) {
			t.Fatal("invalid or over-budget observation accepted")
		}
	}
	labels := baselineMetricLabels(t, "astronomer_cluster_registration_phase")
	if len(labels) != 2 {
		t.Fatalf("refused result partially changed prior/current owners: %v", labels)
	}
	if got := vaultMetricCount(t, "astronomer_registration_metric_last_complete_timestamp_seconds", "", ""); got != float64(initial.Unix()) {
		t.Fatalf("refused observation changed timestamp=%v", got)
	}
	// A fresh complete observation may retire old owners before admitting a full
	// new set, without temporarily exceeding the cap or retaining old children.
	if !PublishRegistrationMetricSnapshot(rows, RegistrationMetricsWatermark(), time.Unix(400, 0)) {
		t.Fatal("atomic retirement did not release capacity")
	}
	registrationMetricOwners.Lock()
	count := len(registrationMetricOwners.rows)
	registrationMetricOwners.Unlock()
	if count > MetricOwnerMax {
		t.Fatal("registry transient or final overflow")
	}
	if labels := baselineMetricLabels(t, "astronomer_cluster_registration_phase"); len(labels) != len(rows) {
		t.Fatalf("complete phase samples=%d want%d", len(labels), len(rows))
	}
}
