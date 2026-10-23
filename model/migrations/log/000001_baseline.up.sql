-- Baseline of the log schema: the tables GORM AutoMigrate created up to
-- commit 732e212f3, the last release before versioned migrations. A database
-- that release created is adopted at this version instead of running it.

CREATE TABLE audit_logs (
    id bigserial NOT NULL,
    event_id varchar(64),
    user_id bigint,
    username varchar(64),
    actor_role bigint,
    created_at bigint,
    category varchar(24),
    action varchar(128),
    token_ref varchar(64),
    auth_method varchar(24),
    ip varchar(64),
    user_agent varchar(512),
    method varchar(16),
    route varchar(255),
    status bigint,
    success boolean,
    request_id varchar(64),
    content text,
    other json,
    CONSTRAINT audit_logs_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_audit_logs_category ON audit_logs (category);
CREATE INDEX idx_audit_logs_created_at ON audit_logs (created_at);
CREATE UNIQUE INDEX idx_audit_logs_event_id ON audit_logs (event_id);
CREATE INDEX idx_audit_logs_request_id ON audit_logs (request_id);
CREATE INDEX idx_audit_logs_username ON audit_logs (username);
CREATE INDEX idx_audit_time_event ON audit_logs (created_at, event_id);
CREATE INDEX idx_audit_token_time ON audit_logs (token_ref, created_at);
CREATE INDEX idx_audit_user_time ON audit_logs (user_id, created_at);

CREATE TABLE logs (
    id bigserial NOT NULL,
    user_id bigint,
    created_at bigint,
    type bigint,
    content text,
    username text DEFAULT ''::text,
    token_name text DEFAULT ''::text,
    model_name text DEFAULT ''::text,
    quota bigint DEFAULT 0,
    prompt_tokens bigint DEFAULT 0,
    completion_tokens bigint DEFAULT 0,
    use_time bigint DEFAULT 0,
    is_stream boolean,
    channel_id bigint,
    channel_name text,
    token_id bigint DEFAULT 0,
    "group" text,
    ip text DEFAULT ''::text,
    request_id varchar(64) DEFAULT ''::character varying,
    upstream_request_id varchar(128) DEFAULT ''::character varying,
    other text,
    CONSTRAINT logs_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_created_at_id ON logs (created_at, id);
CREATE INDEX idx_created_at_type ON logs (created_at, type);
CREATE INDEX idx_logs_channel_id ON logs (channel_id);
CREATE INDEX idx_logs_group ON logs ("group");
CREATE INDEX idx_logs_request_id ON logs (request_id);
CREATE INDEX idx_logs_token_id ON logs (token_id);
CREATE INDEX idx_logs_token_name ON logs (token_name);
CREATE INDEX idx_logs_upstream_request_id ON logs (upstream_request_id) WHERE ((upstream_request_id)::text <> ''::text);
CREATE INDEX idx_logs_username ON logs (username);
CREATE INDEX idx_user_id_id ON logs (user_id, id);
CREATE INDEX index_username_model_name ON logs (model_name, username);
