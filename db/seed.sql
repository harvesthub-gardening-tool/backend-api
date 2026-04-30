-- Development seed data — DO NOT use in production.
-- Runs once on first container init (docker-entrypoint-initdb.d/).
--
-- NOTE: auth_users, hub_tokens, hubs, sensor_nodes are managed by GORM AutoMigrate
-- and created by the Go app at startup — not at DB init time.
-- Only tables defined in schema.sql (sensor_data) can be seeded here.

-- 48 hours of readings at 15-minute intervals for node-1 and node-2.
INSERT INTO sensor_data (
    time,
    node_id,
    air_temperature,
    air_pressure,
    air_humidity,
    soil_temperature,
    soil_humidity
)
SELECT
    t,
    'node-1',
    25 + (random() * 5),
    101000 + (random() * 800),
    60 + (random() * 10),
    20 + (random() * 4),
    30 + (random() * 5)
FROM generate_series(NOW() - INTERVAL '48 hours', NOW(), INTERVAL '15 minutes') AS t;

INSERT INTO sensor_data (
    time,
    node_id,
    air_temperature,
    air_pressure,
    air_humidity,
    soil_temperature,
    soil_humidity
)
SELECT
    t,
    'node-2',
    24 + (random() * 5),
    100800 + (random() * 900),
    62 + (random() * 10),
    19 + (random() * 4),
    28 + (random() * 5)
FROM generate_series(NOW() - INTERVAL '48 hours', NOW(), INTERVAL '15 minutes') AS t;
