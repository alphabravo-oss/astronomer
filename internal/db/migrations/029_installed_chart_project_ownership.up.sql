ALTER TABLE public.installed_charts
    ADD COLUMN project_id uuid REFERENCES public.projects(id) ON DELETE RESTRICT;

CREATE INDEX idx_installed_charts_project
    ON public.installed_charts (project_id, created_at DESC)
    WHERE project_id IS NOT NULL;

