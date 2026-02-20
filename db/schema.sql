-- TimescaleDB schema for Harvest Hub
-- Runs once on first container init (docker-entrypoint-initdb.d/).
-- auth_users and hub_tokens are intentionally absent: GORM AutoMigrate owns them.

CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE IF NOT EXISTS sensor_data (
    time          TIMESTAMPTZ      NOT NULL,
    node_id       TEXT             NOT NULL,
    temperature   DOUBLE PRECISION,
    humidity      DOUBLE PRECISION,
    soil_moisture DOUBLE PRECISION
);

SELECT create_hypertable('sensor_data', 'time', if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_sensor_node_time ON sensor_data (node_id, time DESC);
