-- PostgreSQL initialization script for note-memory
-- This runs automatically on first container start via docker-compose

-- Enable pgvector extension (required for semantic search)
CREATE EXTENSION IF NOT EXISTS vector;

-- Enable pg_trgm for better text search (optional, used by GIN indexes)
CREATE EXTENSION IF NOT EXISTS pg_trgm;
