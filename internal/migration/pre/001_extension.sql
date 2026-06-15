-- Pre-migration: PostgreSQL extensions
-- Must run before GORM AutoMigrate so vector type is available for table creation.

CREATE EXTENSION IF NOT EXISTS vector;
