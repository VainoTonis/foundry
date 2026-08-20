-- Replace plans.project_id/repo_name single-owner columns with an ordered
-- many-to-many plan_repositories membership table. Position 0 is the
-- primary repository for a plan.

CREATE TABLE plan_repositories (
    plan_id    BIGINT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    position   INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (plan_id, position),
    UNIQUE (plan_id, project_id)
);

CREATE INDEX ON plan_repositories(project_id);

-- Ensure any legacy repo_name referenced below that does not already have
-- a matching project row exists before backfilling. Each of these is
-- create-if-missing by projects.name; projects already present (looked up
-- by name) are left untouched.
INSERT INTO projects (name, repo_path, remote_url)
SELECT 'personal/kickstartonator', '/home/tonis/git/personal/kickstartonator', 'ssh://git@github.com/VainoTonis/kickstartonator.git'
WHERE NOT EXISTS (SELECT 1 FROM projects WHERE name = 'personal/kickstartonator');

INSERT INTO projects (name, repo_path, remote_url)
SELECT 'personal/smoke-test', NULL, 'ssh://git@github.com/VainoTonis/smoke-test.git'
WHERE NOT EXISTS (SELECT 1 FROM projects WHERE name = 'personal/smoke-test');

INSERT INTO projects (name, repo_path, remote_url)
SELECT 'easyparkgroup/backstage', '/home/tonis/git/arrive/backstage', 'ssh://git@github.com/easyparkgroup/backstage.git'
WHERE NOT EXISTS (SELECT 1 FROM projects WHERE name = 'easyparkgroup/backstage');

INSERT INTO projects (name, repo_path, remote_url)
SELECT 'easyparkgroup/fleet-memory', '/home/tonis/git/arrive/fleet-memory', 'ssh://git@github.com/easyparkgroup/fleet-memory.git'
WHERE NOT EXISTS (SELECT 1 FROM projects WHERE name = 'easyparkgroup/fleet-memory');

-- Backfill plan_repositories, position 0, one row per plan.
DO $$
DECLARE
    plan_rec RECORD;
    resolved_project_id BIGINT;
BEGIN
    FOR plan_rec IN SELECT id, project_id, repo_name FROM plans LOOP
        IF plan_rec.project_id IS NOT NULL THEN
            resolved_project_id := plan_rec.project_id;
        ELSE
            resolved_project_id := CASE plan_rec.repo_name
                WHEN 'foundry' THEN (SELECT id FROM projects WHERE name = 'personal/foundry')
                WHEN 'VainoTonis/foundry' THEN (SELECT id FROM projects WHERE name = 'personal/foundry')
                WHEN 'personal/foundry' THEN (SELECT id FROM projects WHERE name = 'personal/foundry')
                WHEN 'tonis2/foundry' THEN (SELECT id FROM projects WHERE name = 'personal/foundry')
                WHEN 'VainoTonis/cerberus' THEN (SELECT id FROM projects WHERE name = 'personal/cerberus')
                WHEN 'VainoTonis/kickstartonator' THEN (SELECT id FROM projects WHERE name = 'personal/kickstartonator')
                WHEN 'VainoTonis/smoke-test' THEN (SELECT id FROM projects WHERE name = 'personal/smoke-test')
                WHEN 'easyparkgroup/backstage' THEN (SELECT id FROM projects WHERE name = 'easyparkgroup/backstage')
                WHEN 'easyparkgroup/fleet-memory' THEN (SELECT id FROM projects WHERE name = 'easyparkgroup/fleet-memory')
                WHEN 'fleet-memory' THEN (SELECT id FROM projects WHERE name = 'easyparkgroup/fleet-memory')
                ELSE NULL
            END;

            IF resolved_project_id IS NULL THEN
                RAISE EXCEPTION 'migration 028: unmapped repo_name % for plan id %', plan_rec.repo_name, plan_rec.id;
            END IF;
        END IF;

        INSERT INTO plan_repositories (plan_id, project_id, position)
        VALUES (plan_rec.id, resolved_project_id, 0);
    END LOOP;
END $$;

-- Verify every plan ended up with a position-0 row.
DO $$
DECLARE
    missing_count BIGINT;
BEGIN
    SELECT COUNT(*) INTO missing_count
    FROM plans p
    WHERE NOT EXISTS (
        SELECT 1 FROM plan_repositories pr WHERE pr.plan_id = p.id AND pr.position = 0
    );

    IF missing_count > 0 THEN
        RAISE EXCEPTION 'migration 028: % plan(s) left without a position-0 plan_repositories row', missing_count;
    END IF;
END $$;

DROP INDEX IF EXISTS plans_project_id_idx;
DROP INDEX IF EXISTS plans_repo_name_idx;
ALTER TABLE plans DROP COLUMN project_id;
ALTER TABLE plans DROP COLUMN repo_name;
