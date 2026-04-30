-- TimescaleDB-specific schema for Harvest Hub.
-- Runs once on first container init (docker-entrypoint-initdb.d/).
--
-- Domain tables (auth_users, hub_tokens, hubs, sensor_nodes) are intentionally
-- absent: GORM AutoMigrate in server/main.go owns their lifecycle.
-- Only sensor_data requires manual setup because TimescaleDB hypertables
-- cannot be created via GORM.

CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE IF NOT EXISTS sensor_data (
    time             TIMESTAMPTZ      NOT NULL,
    node_id          TEXT             NOT NULL,
    air_temperature  DOUBLE PRECISION,
    air_pressure     DOUBLE PRECISION,
    air_humidity     DOUBLE PRECISION,
    soil_temperature DOUBLE PRECISION,
    soil_humidity    DOUBLE PRECISION
);

SELECT create_hypertable('sensor_data', 'time', if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_sensor_node_time ON sensor_data (node_id, time DESC);
