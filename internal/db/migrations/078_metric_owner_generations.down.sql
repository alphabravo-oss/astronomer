-- Removing this telemetry-only registry does not delete or rewrite domain
-- rows. New readers require schema78+; roll back binaries before this contract.
DROP TRIGGER sso_configurations_metric_owner_truncate ON public.sso_configurations;
DROP TRIGGER sso_configurations_metric_owner_lifecycle ON public.sso_configurations;
DROP TRIGGER siem_forwarders_metric_owner_truncate ON public.siem_forwarders;
DROP TRIGGER siem_forwarders_metric_owner_lifecycle ON public.siem_forwarders;
DROP TRIGGER cluster_groups_metric_owner_truncate ON public.cluster_groups;
DROP TRIGGER cluster_groups_metric_owner_lifecycle ON public.cluster_groups;
DROP TRIGGER clusters_metric_owner_truncate ON public.clusters;
DROP TRIGGER clusters_metric_owner_lifecycle ON public.clusters;
DROP FUNCTION public.truncate_metric_owner_generations();
DROP FUNCTION public.maintain_metric_owner_generation();
DROP TABLE public.metric_owner_generations;
