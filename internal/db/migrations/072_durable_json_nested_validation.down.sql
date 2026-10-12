-- Reverse the expand stage without changing any customer row or older registry.
ALTER TABLE public.delivery_system_rollouts DROP CONSTRAINT delivery_system_rollouts_strategy_v1;
ALTER TABLE public.delivery_rollouts DROP CONSTRAINT delivery_rollouts_frozen_strategy_v1;
ALTER TABLE public.delivery_rollouts DROP CONSTRAINT delivery_rollouts_strategy_v1;
ALTER TABLE public.delivery_targets DROP CONSTRAINT delivery_targets_placement_v1;
ALTER TABLE public.logging_pipelines DROP CONSTRAINT logging_pipelines_labels_v1;
ALTER TABLE public.task_outbox DROP CONSTRAINT task_outbox_payload_v1;
ALTER TABLE public.alert_inhibitions DROP CONSTRAINT alert_inhibitions_nested_v1;
DROP FUNCTION public.durable_json_task_payload_v1(bytea);
DROP FUNCTION public.durable_json_rollout_strategy_v1(jsonb);
DROP FUNCTION public.durable_json_amount_v1(jsonb);
DROP FUNCTION public.durable_json_placement_v1(jsonb);
DROP FUNCTION public.durable_json_inhibition_v1(jsonb,jsonb,jsonb);
DROP FUNCTION public.durable_json_string_array_v1(jsonb);
DROP FUNCTION public.durable_json_string_map_v1(jsonb);
