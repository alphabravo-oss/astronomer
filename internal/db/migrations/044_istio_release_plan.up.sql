-- Official ordered Helm installation, pinned 2026-09-10.
-- https://istio.io/latest/docs/setup/install/helm/
-- Values verified against istio/istio tag 1.31.0 chart sources.
INSERT INTO public.cluster_tools
    (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, presets)
VALUES (
    'e8c42de5-d35b-4994-b3f5-b30fac0b9b03', 'istio', 'Istio',
    'Sidecar service mesh control plane. Installs base CRDs before istiod and removes releases in reverse order. Requires an admin-profile agent. Namespace injection remains opt-in; an ingress gateway is not included.',
    'network', 'mesh',
    '[{"order":0,"repo_url":"https://blob.istio.io/istio-release/charts","namespace":"istio-system","chart_name":"base","release_name":"istio-base","version":"1.31.0","values_key":"base"},{"order":1,"repo_url":"https://blob.istio.io/istio-release/charts","namespace":"istio-system","chart_name":"istiod","release_name":"istiod","version":"1.31.0","values_key":"istiod"}]',
    '1.31.0', 'istio-system',
    '{"default":{"base":{"defaultRevision":"default"},"istiod":{"autoscaleEnabled":true,"autoscaleMin":2,"autoscaleMax":5,"replicaCount":2,"global":{"hub":"docker.io/istio","tag":"1.31.0"},"sidecarInjectorWebhook":{"enableNamespacesByDefault":false}}},"development":{"base":{"defaultRevision":"default"},"istiod":{"autoscaleEnabled":false,"replicaCount":1,"global":{"hub":"docker.io/istio","tag":"1.31.0"},"sidecarInjectorWebhook":{"enableNamespacesByDefault":false}}}}'
)
ON CONFLICT (slug) DO NOTHING;
