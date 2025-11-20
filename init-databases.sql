-- Initialize garden_db with TimescaleDB
\c garden_db;

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Garden sensor data (TimescaleDB hypertable)
CREATE TABLE sensor_data (
    time TIMESTAMPTZ NOT NULL,
    node_id TEXT NOT NULL,
    temperature DOUBLE PRECISION,
    humidity DOUBLE PRECISION,
    soil_moisture DOUBLE PRECISION
);

SELECT create_hypertable('sensor_data', 'time');

CREATE INDEX idx_sensor_node_time ON sensor_data (node_id, time DESC);

-- User metadata (references Zitadel users)
CREATE TABLE users (
    user_id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Hub metadata (track service account)
CREATE TABLE hub_metadata (
    hub_id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    location TEXT,
    registered_at TIMESTAMPTZ DEFAULT NOW(),
    last_seen TIMESTAMPTZ
);

CREATE INDEX idx_hub_last_seen ON hub_metadata (last_seen DESC);

-- Insert sample data for the last 24 hours at 15-minute intervals for node 'node-1'
INSERT INTO sensor_data (time, node_id, temperature, humidity, soil_moisture)
SELECT 
    time,
    'node-1' AS node_id,
    20 + random() * 10 AS temperature,
    40 + random() * 20 AS humidity,
    30 + random() * 30 AS soil_moisture
FROM generate_series(
    NOW() - INTERVAL '24 hours',
    NOW(),
    INTERVAL '15 minutes'
) AS time;
