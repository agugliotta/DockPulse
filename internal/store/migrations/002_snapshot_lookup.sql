CREATE INDEX idx_snapshots_container_state
ON image_snapshots(container_id, id DESC);
