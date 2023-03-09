{
  apiVersion: 'v1',
  kind: 'ConfigMap',
  metadata: {
    annotations: {
      'include.release.openshift.io/self-managed-high-availability': 'true',
      'include.release.openshift.io/single-node-developer': 'true',
      'release.openshift.io/delete': "true",
    },
    name: 'grafana-dashboard-etcd',
    namespace: 'openshift-config-managed',
  }
}
