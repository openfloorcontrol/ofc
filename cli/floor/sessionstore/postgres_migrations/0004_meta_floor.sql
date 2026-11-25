-- Sessions belong to a floor by meta.floor. Sessions recorded before
-- that field belong to the floor named like their blueprint.

UPDATE sessions
SET meta = jsonb_set(meta, '{floor}', meta->'blueprint_name')
WHERE meta ? 'blueprint_name' AND NOT meta ? 'floor';
