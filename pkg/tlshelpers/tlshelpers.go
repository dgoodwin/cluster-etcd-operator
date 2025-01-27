package tlshelpers

import (
	"bytes"
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"github.com/openshift/cluster-etcd-operator/pkg/dnshelpers"
	"github.com/openshift/cluster-etcd-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	corev1informers "k8s.io/client-go/informers/core/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/crypto"
	"go.etcd.io/etcd/client/pkg/v3/tlsutil"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
)

const (
	etcdCertValidity          = 3 * 365 * 24 * time.Hour
	etcdCertValidityRefresh   = 2.5 * 365 * 24 * time.Hour
	etcdCaCertValidity        = 5 * 365 * 24 * time.Hour
	etcdCaCertValidityRefresh = 4.5 * 365 * 24 * time.Hour

	peerOrg   = "system:etcd-peers"
	serverOrg = "system:etcd-servers"
	metricOrg = "system:etcd-metrics"

	// TODO debt left for @hexfusion or @sanchezl
	fakePodFQDN = "etcd-client"

	EtcdJiraComponentName                  = "etcd"
	EtcdSignerCertSecretName               = "etcd-signer"
	EtcdSignerCaBundleConfigMapName        = "etcd-ca-bundle"
	EtcdMetricsSignerCertSecretName        = "etcd-metric-signer"
	EtcdMetricsSignerCaBundleConfigMapName = "etcd-metrics-ca-bundle"
	EtcdAllCertsSecretName                 = "etcd-all-certs"
	EtcdClientCertSecretName               = "etcd-client"
	EtcdMetricsClientCertSecretName        = "etcd-metric-client"
)

func GetPeerClientSecretNameForNode(nodeName string) string {
	return fmt.Sprintf("etcd-peer-%s", nodeName)
}
func GetServingSecretNameForNode(nodeName string) string {
	return fmt.Sprintf("etcd-serving-%s", nodeName)
}
func GetServingMetricsSecretNameForNode(nodeName string) string {
	return fmt.Sprintf("etcd-serving-metrics-%s", nodeName)
}

func getPeerHostNames(nodeInternalIPs []string) []string {
	return append([]string{"localhost"}, nodeInternalIPs...)
}

func getServerHostNames(nodeInternalIPs []string) []string {
	return append([]string{
		"localhost",
		"etcd.kube-system.svc",
		"etcd.kube-system.svc.cluster.local",
		"etcd.openshift-etcd.svc",
		"etcd.openshift-etcd.svc.cluster.local",
		"127.0.0.1",
		"::1",
		// "0:0:0:0:0:0:0:1" will be automatically collapsed to "::1", so we don't have to add it on top
	}, nodeInternalIPs...)
}

func CreateSignerCertRotationBundleConfigMap(
	cmInformer corev1informers.ConfigMapInformer,
	cmLister corev1listers.ConfigMapLister,
	cmGetter corev1client.ConfigMapsGetter,
	recorder events.Recorder) certrotation.CABundleConfigMap {

	return certrotation.CABundleConfigMap{
		Name:          EtcdSignerCaBundleConfigMapName,
		Namespace:     operatorclient.TargetNamespace,
		JiraComponent: EtcdJiraComponentName,
		Description:   "bundle for etcd signer certificate authorities",
		Informer:      cmInformer,
		Lister:        cmLister,
		Client:        cmGetter,
		EventRecorder: recorder,
	}
}

func CreateMetricsSignerCertRotationBundleConfigMap(
	cmInformer corev1informers.ConfigMapInformer,
	cmLister corev1listers.ConfigMapLister,
	cmGetter corev1client.ConfigMapsGetter,
	recorder events.Recorder) certrotation.CABundleConfigMap {

	return certrotation.CABundleConfigMap{
		Name:          EtcdMetricsSignerCaBundleConfigMapName,
		Namespace:     operatorclient.TargetNamespace,
		JiraComponent: EtcdJiraComponentName,
		Description:   "bundle for etcd metrics signer certificate authorities",
		Informer:      cmInformer,
		Lister:        cmLister,
		Client:        cmGetter,
		EventRecorder: recorder,
	}
}

func CreateSignerCert(
	secretInformer corev1informers.SecretInformer,
	secretLister corev1listers.SecretLister,
	secretGetter corev1client.SecretsGetter,
	recorder events.Recorder) certrotation.RotatedSigningCASecret {

	return certrotation.RotatedSigningCASecret{
		Namespace:     operatorclient.TargetNamespace,
		Name:          EtcdSignerCertSecretName,
		JiraComponent: EtcdJiraComponentName,
		Description:   "etcd signer certificate authorities",
		Validity:      etcdCaCertValidity,
		Refresh:       etcdCaCertValidityRefresh,

		Informer:      secretInformer,
		Lister:        secretLister,
		Client:        secretGetter,
		EventRecorder: recorder,
	}
}

func CreateMetricsSignerCert(
	secretInformer corev1informers.SecretInformer,
	secretLister corev1listers.SecretLister,
	secretGetter corev1client.SecretsGetter,
	recorder events.Recorder) certrotation.RotatedSigningCASecret {

	return certrotation.RotatedSigningCASecret{
		Namespace:     operatorclient.TargetNamespace,
		Name:          EtcdMetricsSignerCertSecretName,
		JiraComponent: EtcdJiraComponentName,
		Description:   "etcd metrics signer certificate authorities",
		Validity:      etcdCaCertValidity,
		Refresh:       etcdCaCertValidityRefresh,

		Informer:      secretInformer,
		Lister:        secretLister,
		Client:        secretGetter,
		EventRecorder: recorder,
	}
}

func CreatePeerCertificate(node *corev1.Node,
	secretInformer corev1informers.SecretInformer,
	secretLister corev1listers.SecretLister,
	secretGetter corev1client.SecretsGetter,
	recorder events.Recorder) (*certrotation.RotatedSelfSignedCertKeySecret, error) {
	return createCertForNode(
		fmt.Sprintf("Peer Cert for node %s", node.Name),
		GetPeerClientSecretNameForNode(node.Name),
		node, secretInformer, secretLister, secretGetter, recorder)
}

func CreateServingCertificate(node *corev1.Node,
	secretInformer corev1informers.SecretInformer,
	secretLister corev1listers.SecretLister,
	secretGetter corev1client.SecretsGetter,
	recorder events.Recorder) (*certrotation.RotatedSelfSignedCertKeySecret, error) {
	return createCertForNode(
		fmt.Sprintf("Serving Cert for node %s", node.Name),
		GetServingSecretNameForNode(node.Name),
		node, secretInformer, secretLister, secretGetter, recorder)
}

func CreateMetricsServingCertificate(node *corev1.Node,
	secretInformer corev1informers.SecretInformer,
	secretLister corev1listers.SecretLister,
	secretGetter corev1client.SecretsGetter,
	recorder events.Recorder) (*certrotation.RotatedSelfSignedCertKeySecret, error) {
	return createCertForNode(
		fmt.Sprintf("Metric Serving Cert for node %s", node.Name),
		GetServingMetricsSecretNameForNode(node.Name),
		node, secretInformer, secretLister, secretGetter, recorder)
}

func createCertForNode(description, secretName string, node *corev1.Node,
	secretInformer corev1informers.SecretInformer,
	secretLister corev1listers.SecretLister,
	secretGetter corev1client.SecretsGetter,
	recorder events.Recorder) (*certrotation.RotatedSelfSignedCertKeySecret, error) {

	ipAddresses, err := dnshelpers.GetInternalIPAddressesForNodeName(node)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve internal IP addresses for node: %w", err)
	}
	hostNames := getServerHostNames(ipAddresses)

	creator := &certrotation.ServingRotation{
		Hostnames: func() []string {
			return hostNames
		},
		CertificateExtensionFn: []crypto.CertificateExtensionFunc{
			func(certificate *x509.Certificate) error {
				certificate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
				return nil
			},
		},
	}

	return &certrotation.RotatedSelfSignedCertKeySecret{
		Namespace:     operatorclient.TargetNamespace,
		Name:          secretName,
		JiraComponent: EtcdJiraComponentName,
		Description:   description,
		Validity:      etcdCertValidity,
		Refresh:       etcdCertValidityRefresh,
		CertCreator:   creator,

		Informer:      secretInformer,
		Lister:        secretLister,
		Client:        secretGetter,
		EventRecorder: recorder,
	}, nil
}

func CreateMetricsClientCert(
	secretInformer corev1informers.SecretInformer,
	secretLister corev1listers.SecretLister,
	secretGetter corev1client.SecretsGetter,
	recorder events.Recorder) certrotation.RotatedSelfSignedCertKeySecret {
	creator := &certrotation.ClientRotation{
		UserInfo: &user.DefaultInfo{
			Name:   "etcd-metric",
			Groups: []string{"system:etcd", "etcd-metric"},
		},
	}

	return certrotation.RotatedSelfSignedCertKeySecret{
		Namespace:     operatorclient.TargetNamespace,
		Name:          EtcdMetricsClientCertSecretName,
		JiraComponent: EtcdJiraComponentName,
		Description:   "etcd metrics client certificate",
		Validity:      etcdCertValidity,
		Refresh:       etcdCertValidityRefresh,
		CertCreator:   creator,

		Informer:      secretInformer,
		Lister:        secretLister,
		Client:        secretGetter,
		EventRecorder: recorder,
	}
}

func CreateEtcdClientCert(
	secretInformer corev1informers.SecretInformer,
	secretLister corev1listers.SecretLister,
	secretGetter corev1client.SecretsGetter,
	recorder events.Recorder) certrotation.RotatedSelfSignedCertKeySecret {
	creator := &certrotation.ClientRotation{
		UserInfo: &user.DefaultInfo{
			Name:   "etcd-client",
			Groups: []string{"system:etcd", "etcd-client"},
		},
	}

	return certrotation.RotatedSelfSignedCertKeySecret{
		Namespace:     operatorclient.TargetNamespace,
		Name:          EtcdClientCertSecretName,
		JiraComponent: EtcdJiraComponentName,
		Description:   "etcd client certificate",
		Validity:      etcdCertValidity,
		Refresh:       etcdCertValidityRefresh,
		CertCreator:   creator,

		Informer:      secretInformer,
		Lister:        secretLister,
		Client:        secretGetter,
		EventRecorder: recorder,
	}
}

func ReadConfigSignerCert(ctx context.Context, secretClient corev1client.SecretsGetter) (*crypto.CA, error) {
	signingCertKeyPairSecret, err := secretClient.Secrets(operatorclient.GlobalUserSpecifiedConfigNamespace).Get(ctx, EtcdSignerCertSecretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("error getting %s/%s: %w", operatorclient.GlobalUserSpecifiedConfigNamespace, EtcdSignerCertSecretName, err)
	}

	return crypto.GetCAFromBytes(signingCertKeyPairSecret.Data["tls.crt"], signingCertKeyPairSecret.Data["tls.key"])
}

func ReadConfigMetricsSignerCert(ctx context.Context, secretClient corev1client.SecretsGetter) (*crypto.CA, error) {
	metricsSigningCertKeyPairSecret, err := secretClient.Secrets(operatorclient.GlobalUserSpecifiedConfigNamespace).Get(ctx, EtcdMetricsSignerCertSecretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("error getting %s/%s: %w", operatorclient.GlobalUserSpecifiedConfigNamespace, EtcdMetricsSignerCertSecretName, err)
	}

	return crypto.GetCAFromBytes(metricsSigningCertKeyPairSecret.Data["tls.crt"], metricsSigningCertKeyPairSecret.Data["tls.key"])
}

func CreatePeerCertKey(caCert, caKey []byte, nodeInternalIPs []string) (*bytes.Buffer, *bytes.Buffer, error) {
	return createNewCombinedClientAndServingCerts(caCert, caKey, fakePodFQDN, peerOrg, getPeerHostNames(nodeInternalIPs))
}

func CreateServerCertKey(caCert, caKey []byte, nodeInternalIPs []string) (*bytes.Buffer, *bytes.Buffer, error) {
	return createNewCombinedClientAndServingCerts(caCert, caKey, fakePodFQDN, serverOrg, getServerHostNames(nodeInternalIPs))
}

func CreateMetricCertKey(caCert, caKey []byte, nodeInternalIPs []string) (*bytes.Buffer, *bytes.Buffer, error) {
	return createNewCombinedClientAndServingCerts(caCert, caKey, fakePodFQDN, metricOrg, getServerHostNames(nodeInternalIPs))
}

func createNewCombinedClientAndServingCerts(caCert, caKey []byte, podFQDN, org string, hostNames []string) (*bytes.Buffer, *bytes.Buffer, error) {
	etcdCAKeyPair, err := crypto.GetCAFromBytes(caCert, caKey)
	if err != nil {
		return nil, nil, err
	}

	certConfig, err := etcdCAKeyPair.MakeServerCertForDuration(sets.NewString(hostNames...), etcdCertValidity, func(cert *x509.Certificate) error {
		cert.Subject = pkix.Name{
			Organization: []string{org},
			CommonName:   strings.TrimSuffix(org, "s") + ":" + podFQDN,
		}
		cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}

		// TODO: Extended Key Usage:
		// All profiles expect a x509.ExtKeyUsageCodeSigning set on extended Key Usages
		// need to investigage: https://github.com/etcd-io/etcd/issues/9398#issuecomment-435340312

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	certBytes := &bytes.Buffer{}
	keyBytes := &bytes.Buffer{}
	if err := certConfig.WriteCertConfig(certBytes, keyBytes); err != nil {
		return nil, nil, err
	}
	return certBytes, keyBytes, nil
}

func SupportedEtcdCiphers(cipherSuites []string) []string {
	allowedCiphers := []string{}
	for _, cipher := range cipherSuites {
		_, ok := tlsutil.GetCipherSuite(cipher)
		if !ok {
			// skip and log unsupported ciphers
			klog.Warningf("cipher is not supported for use with etcd, skipping: %q", cipher)
			continue
		}
		allowedCiphers = append(allowedCiphers, cipher)
	}
	return allowedCiphers

}
