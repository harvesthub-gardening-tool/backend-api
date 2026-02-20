-- Development seed data — DO NOT use in production.
-- Inserted only on first container init (docker-entrypoint-initdb.d/).
-- Provides ~24 h of realistic sensor readings for two nodes.

INSERT INTO sensor_data (time, node_id, temperature, humidity, soil_moisture)
SELECT
    t,
    'node-1',
    25 + (random() * 5),
    60 + (random() * 10),
    30 + (random() * 5)
FROM generate_series(NOW() - INTERVAL '24 hours', NOW(), INTERVAL '15 minutes') AS t;

INSERT INTO sensor_data (time, node_id, temperature, humidity, soil_moisture)
SELECT
    t,
    'node-2',
    24 + (random() * 5),
    62 + (random() * 10),
    28 + (random() * 5)
FROM generate_series(NOW() - INTERVAL '24 hours', NOW(), INTERVAL '15 minutes') AS t;
