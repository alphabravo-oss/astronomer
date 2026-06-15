package kubeutil

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

func DeleteOptions() metav1.DeleteOptions {
	return metav1.DeleteOptions{}
}

func DeleteOptionsWithPropagation(policy metav1.DeletionPropagation) metav1.DeleteOptions {
	return metav1.DeleteOptions{PropagationPolicy: &policy}
}

func DeleteOptionsWithGracePeriod(seconds int64) metav1.DeleteOptions {
	return metav1.DeleteOptions{GracePeriodSeconds: &seconds}
}

func ForegroundDeleteOptions() metav1.DeleteOptions {
	return DeleteOptionsWithPropagation(metav1.DeletePropagationForeground)
}

func BackgroundDeleteOptions() metav1.DeleteOptions {
	return DeleteOptionsWithPropagation(metav1.DeletePropagationBackground)
}

func OrphanDeleteOptions() metav1.DeleteOptions {
	return DeleteOptionsWithPropagation(metav1.DeletePropagationOrphan)
}
