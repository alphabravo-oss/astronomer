ALTER TABLE public.logging_pipeline_outputs
    DROP CONSTRAINT logging_pipeline_outputs_logging_output_id_fkey;

ALTER TABLE public.logging_pipeline_outputs
    ADD CONSTRAINT logging_pipeline_outputs_logging_output_id_fkey
    FOREIGN KEY (logging_output_id)
    REFERENCES public.logging_outputs(id)
    ON DELETE CASCADE;
