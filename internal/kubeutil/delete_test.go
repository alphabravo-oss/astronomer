package kubeutil

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDeleteOptions(t *testing.T) {
	opts := DeleteOptions()
	if opts.PropagationPolicy != nil || opts.GracePeriodSeconds != nil {
		t.Fatalf("DeleteOptions should be empty: %#v", opts)
	}
}

func TestDeleteOptionsWithPropagation(t *testing.T) {
	opts := DeleteOptionsWithPropagation(metav1.DeletePropagationForeground)
	if opts.PropagationPolicy == nil || *opts.PropagationPolicy != metav1.DeletePropagationForeground {
		t.Fatalf("PropagationPolicy = %#v, want foreground", opts.PropagationPolicy)
	}
}

func TestDeleteOptionsWithGracePeriod(t *testing.T) {
	opts := DeleteOptionsWithGracePeriod(3)
	if opts.GracePeriodSeconds == nil || *opts.GracePeriodSeconds != 3 {
		t.Fatalf("GracePeriodSeconds = %#v, want 3", opts.GracePeriodSeconds)
	}
}
