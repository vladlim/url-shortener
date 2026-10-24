CREATE TABLE urls (
    code         text PRIMARY KEY,
    client_id    text NOT NULL,
    external_id  text NOT NULL,
    original_url text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,

    CONSTRAINT urls_client_external_unique UNIQUE (client_id, external_id),
    CONSTRAINT urls_code_not_empty CHECK (length(btrim(code)) > 0),
    CONSTRAINT urls_client_id_not_empty CHECK (length(btrim(client_id)) > 0),
    CONSTRAINT urls_external_id_not_empty CHECK (length(btrim(external_id)) > 0),
    CONSTRAINT urls_original_url_not_empty CHECK (length(btrim(original_url)) > 0),
    CONSTRAINT urls_expiration_after_creation CHECK (expires_at > created_at)
);

-- Used by the periodic cleanup of expired links.
CREATE INDEX urls_expires_at_idx ON urls (expires_at);
