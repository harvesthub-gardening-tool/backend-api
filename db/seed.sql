-- Development seed data — DO NOT use in production.
-- Runs once on first container init (docker-entrypoint-initdb.d/).
--
-- NOTE: auth_users, hub_tokens, hubs, sensor_nodes are managed by GORM AutoMigrate
-- and created by the Go app at startup — not at DB init time.
-- Only tables defined in schema.sql (sensor_data) can be seeded here.

-- 48 hours of readings at 15-minute intervals for node-1 and node-2.
INSERT INTO sensor_data (time, node_id, temperature, humidity, soil_moisture)
SELECT
    t,
    'node-1',
    25 + (random() * 5),
    60 + (random() * 10),
    30 + (random() * 5)
FROM generate_series(NOW() - INTERVAL '48 hours', NOW(), INTERVAL '15 minutes') AS t;

INSERT INTO sensor_data (time, node_id, temperature, humidity, soil_moisture)
SELECT
    t,
    'node-2',
    24 + (random() * 5),
    62 + (random() * 10),
    28 + (random() * 5)
FROM generate_series(NOW() - INTERVAL '48 hours', NOW(), INTERVAL '15 minutes') AS t;
