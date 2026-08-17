UPDATE feedback SET body = '' WHERE body IS NULL;
ALTER TABLE feedback ALTER COLUMN body SET NOT NULL;
