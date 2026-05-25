ALTER TABLE phases
    ADD COLUMN phase_feedback JSONB NOT NULL DEFAULT '{"result":"","useful_context":[],"problems":[],"suggested_memory":"","confidence":0}'::jsonb;
