package resourcesynccontroller

import (
	"context"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-etcd-operator/pkg/operator/operatorclient"
)

func NewResourceSyncController(
	operatorConfigClient v1helpers.OperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder) (*resourcesynccontroller.ResourceSyncController, error) {

	secretClient := v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces)
	configMapClient := v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces)

	resourceSyncController := resourcesynccontroller.NewResourceSyncController(
		operatorConfigClient,
		kubeInformersForNamespaces,
		secretClient,
		configMapClient,
		eventRecorder,
	)

	if err := resourceSyncController.SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "cluster-config-v1"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.KubeSystemNamespace, Name: "cluster-config-v1"},
	); err != nil {
		return nil, err
	}

	// serving ca
	caBundleExistsFunc := func() (bool, error) {
		return configMapExistsPrecondition(configMapClient, resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-ca-bundle"})
	}
	if err := resourceSyncController.SyncConfigMapConditionally(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "etcd-ca-bundle"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-ca-bundle"},
		caBundleExistsFunc,
	); err != nil {
		return nil, err
	}

	if err := resourceSyncController.SyncConfigMapConditionally(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-peer-client-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-ca-bundle"},
		caBundleExistsFunc,
	); err != nil {
		return nil, err
	}

	// "etcd-serving-ca" is replaced by the "etcd-ca-bundle"
	if err := resourceSyncController.SyncConfigMapConditionally(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-serving-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-ca-bundle"},
		caBundleExistsFunc,
	); err != nil {
		return nil, err
	}

	if err := resourceSyncController.SyncConfigMapConditionally(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: "etcd-serving-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-ca-bundle"},
		caBundleExistsFunc,
	); err != nil {
		return nil, err
	}

	// metrics serving
	metricsBundleExistsFunc := func() (bool, error) {
		return configMapExistsPrecondition(configMapClient, resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-metrics-ca-bundle"})
	}
	// TODO(thomas): copying the metrics ca-bundle back to openshift-config should not be necessary anymore
	// this buys us some more transition time, but the source of truth stays in openshift-etcd
	if err := resourceSyncController.SyncConfigMapConditionally(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: "etcd-metric-serving-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-metrics-ca-bundle"},
		metricsBundleExistsFunc,
	); err != nil {
		return nil, err
	}
	if err := resourceSyncController.SyncConfigMapConditionally(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-metrics-proxy-client-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-metrics-ca-bundle"},
		metricsBundleExistsFunc,
	); err != nil {
		return nil, err
	}
	if err := resourceSyncController.SyncConfigMapConditionally(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "etcd-metric-serving-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-metrics-ca-bundle"},
		metricsBundleExistsFunc,
	); err != nil {
		return nil, err
	}
	if err := resourceSyncController.SyncConfigMapConditionally(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-metrics-proxy-serving-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-metrics-ca-bundle"},
		metricsBundleExistsFunc,
	); err != nil {
		return nil, err
	}

	// client certs
	if err := resourceSyncController.SyncSecret(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "etcd-metric-client"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-metric-client"},
	); err != nil {
		return nil, err
	}

	if err := resourceSyncController.SyncSecret(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "etcd-client"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-client"},
	); err != nil {
		return nil, err
	}

	if err := resourceSyncController.SyncSecret(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: "etcd-client"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "etcd-client"},
	); err != nil {
		return nil, err
	}

	return resourceSyncController, nil
}

// configMapExistsPrecondition will check whether the given resourcesynccontroller.ResourceLocation already exists.
// This is to ensure that the destination is not removed in case we're switching locations, or they are accidentally deleted.
func configMapExistsPrecondition(configMapsGetter corev1client.ConfigMapsGetter, loc resourcesynccontroller.ResourceLocation) (bool, error) {
	_, err := configMapsGetter.ConfigMaps(loc.Namespace).Get(context.Background(), loc.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
