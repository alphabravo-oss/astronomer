-- A pipeline-output link is live routing policy. Cascading it away when an
-- output is deleted can leave an enabled pipeline without a destination and
-- conceal the impact from the operator. Require the pipeline to be edited or
-- removed first so the API can return an explicit resource-in-use conflict.
ALTER TABLE public.logging_pipeline_outputs
    DROP CONSTRAINT logging_pipeline_outputs_logging_output_id_fkey;

ALTER TABLE public.logging_pipeline_outputs
    ADD CONSTRAINT logging_pipeline_outputs_logging_output_id_fkey
    FOREIGN KEY (logging_output_id)
    REFERENCES public.logging_outputs(id)
    ON DELETE RESTRICT;
