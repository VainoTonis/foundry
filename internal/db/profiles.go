package db

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UpdateProfileParams struct {
	Name         *string
	DefaultModel *string
	DefaultImage *string
	AWSProfile   *string
	AWSRegion    *string
	ExtraEnv     map[string]string
}

func CreateProfile(ctx context.Context, pool *pgxpool.Pool, name, defaultModel, defaultImage, awsProfile, awsRegion string, extraEnv map[string]string) (Profile, error) {
	envJSON, err := json.Marshal(extraEnv)
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	var rawEnv []byte
	err = pool.QueryRow(ctx,
		`INSERT INTO profiles (name, default_model, default_image, aws_profile, aws_region, extra_env)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, name, default_model, default_image, aws_profile, aws_region, extra_env, created_at, updated_at`,
		name, defaultModel, defaultImage, awsProfile, awsRegion, envJSON,
	).Scan(&p.ID, &p.Name, &p.DefaultModel, &p.DefaultImage, &p.AWSProfile, &p.AWSRegion, &rawEnv, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Profile{}, err
	}
	if err := json.Unmarshal(rawEnv, &p.ExtraEnv); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func ListProfiles(ctx context.Context, pool *pgxpool.Pool) ([]Profile, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, name, default_model, default_image, aws_profile, aws_region, extra_env, created_at, updated_at
		 FROM profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var profiles []Profile
	for rows.Next() {
		var p Profile
		var rawEnv []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.DefaultModel, &p.DefaultImage, &p.AWSProfile, &p.AWSRegion, &rawEnv, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(rawEnv, &p.ExtraEnv); err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

func scanProfile(row pgx.Row) (Profile, error) {
	var p Profile
	var rawEnv []byte
	err := row.Scan(&p.ID, &p.Name, &p.DefaultModel, &p.DefaultImage, &p.AWSProfile, &p.AWSRegion, &rawEnv, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(rawEnv, &p.ExtraEnv); err != nil {
		return p, err
	}
	return p, nil
}

func GetProfile(ctx context.Context, pool *pgxpool.Pool, id int64) (Profile, error) {
	return scanProfile(pool.QueryRow(ctx,
		`SELECT id, name, default_model, default_image, aws_profile, aws_region, extra_env, created_at, updated_at
		 FROM profiles WHERE id = $1`, id,
	))
}

func GetProfileByName(ctx context.Context, pool *pgxpool.Pool, name string) (Profile, error) {
	return scanProfile(pool.QueryRow(ctx,
		`SELECT id, name, default_model, default_image, aws_profile, aws_region, extra_env, created_at, updated_at
		 FROM profiles WHERE name = $1`, name,
	))
}

func UpdateProfile(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdateProfileParams) (Profile, error) {
	set := []string{"updated_at = NOW()"}
	args := []any{}
	n := 1
	maybeStr := func(field string, v *string) {
		if v != nil {
			set = append(set, field+" = $"+itoa(n))
			args = append(args, *v)
			n++
		}
	}
	maybeStr("name", p.Name)
	maybeStr("default_model", p.DefaultModel)
	maybeStr("default_image", p.DefaultImage)
	maybeStr("aws_profile", p.AWSProfile)
	maybeStr("aws_region", p.AWSRegion)
	if p.ExtraEnv != nil {
		envJSON, err := json.Marshal(p.ExtraEnv)
		if err != nil {
			return Profile{}, err
		}
		set = append(set, "extra_env = $"+itoa(n))
		args = append(args, envJSON)
		n++
	}
	args = append(args, id)
	q := `UPDATE profiles SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) +
		` RETURNING id, name, default_model, default_image, aws_profile, aws_region, extra_env, created_at, updated_at`
	return scanProfile(pool.QueryRow(ctx, q, args...))
}

func DeleteProfile(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	tag, err := pool.Exec(ctx, `DELETE FROM profiles WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
