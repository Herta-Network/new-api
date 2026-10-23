-- Baseline of the main schema: the tables GORM AutoMigrate created up to
-- commit 732e212f3, the last release before versioned migrations. A database
-- that release created is adopted at this version instead of running it.

CREATE TABLE abilities (
    "group" varchar(64) NOT NULL,
    model varchar(255) NOT NULL,
    channel_id bigint NOT NULL,
    enabled boolean,
    priority bigint DEFAULT 0,
    weight bigint DEFAULT 0,
    tag text,
    CONSTRAINT abilities_pkey PRIMARY KEY ("group", model, channel_id)
);
CREATE INDEX idx_abilities_channel_id ON abilities (channel_id);
CREATE INDEX idx_abilities_priority ON abilities (priority);
CREATE INDEX idx_abilities_tag ON abilities (tag);
CREATE INDEX idx_abilities_weight ON abilities (weight);

CREATE TABLE auth_flows (
    id bigserial NOT NULL,
    token_hash character(64) NOT NULL,
    purpose varchar(32) NOT NULL,
    provider varchar(64),
    intent varchar(16),
    user_id bigint,
    session_id varchar(64),
    payload text,
    created_at timestamptz,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT auth_flows_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_auth_flow_purpose_expiry ON auth_flows (purpose, expires_at);
CREATE INDEX idx_auth_flows_consumed_at ON auth_flows (consumed_at);
CREATE INDEX idx_auth_flows_session_id ON auth_flows (session_id);
CREATE UNIQUE INDEX idx_auth_flows_token_hash ON auth_flows (token_hash);
CREATE INDEX idx_auth_flows_user_id ON auth_flows (user_id);

CREATE TABLE authz_roles (
    id bigserial NOT NULL,
    key varchar(64) NOT NULL,
    name varchar(100) NOT NULL,
    description text,
    built_in boolean,
    enabled boolean,
    sort bigint,
    created_at bigint,
    updated_at bigint,
    CONSTRAINT authz_roles_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_authz_roles_key ON authz_roles (key);

CREATE TABLE casbin_rule (
    id bigserial NOT NULL,
    ptype varchar(100),
    v0 varchar(100),
    v1 varchar(100),
    v2 varchar(100),
    v3 varchar(100),
    v4 varchar(100),
    v5 varchar(100),
    CONSTRAINT casbin_rule_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_casbin_rule ON casbin_rule (ptype, v0, v1, v2, v3, v4, v5);
CREATE UNIQUE INDEX idx_casbin_rule_unique ON casbin_rule (ptype, v0, v1, v2, v3, v4, v5);

CREATE TABLE channels (
    id bigserial NOT NULL,
    type bigint DEFAULT 0,
    key text NOT NULL,
    open_ai_organization text,
    test_model text,
    status bigint DEFAULT 1,
    name text,
    weight bigint DEFAULT 0,
    created_time bigint,
    test_time bigint,
    response_time bigint,
    base_url text DEFAULT ''::text,
    other text,
    balance numeric,
    balance_updated_time bigint,
    models text,
    "group" varchar(64) DEFAULT 'default'::character varying,
    used_quota bigint DEFAULT 0,
    model_mapping text,
    status_code_mapping varchar(1024) DEFAULT ''::character varying,
    priority bigint DEFAULT 0,
    auto_ban bigint DEFAULT 1,
    other_info text,
    tag text,
    setting text,
    param_override text,
    header_override text,
    remark varchar(255),
    channel_info json,
    settings text,
    CONSTRAINT channels_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_channels_name ON channels (name);
CREATE INDEX idx_channels_tag ON channels (tag);

CREATE TABLE checkins (
    id bigserial NOT NULL,
    user_id bigint NOT NULL,
    checkin_date varchar(10) NOT NULL,
    quota_awarded bigint NOT NULL,
    created_at bigint,
    CONSTRAINT checkins_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_user_checkin_date ON checkins (user_id, checkin_date);

CREATE TABLE custom_oauth_providers (
    id bigserial NOT NULL,
    name varchar(64) NOT NULL,
    slug varchar(64) NOT NULL,
    icon varchar(128) DEFAULT ''::character varying,
    enabled boolean DEFAULT false,
    client_id varchar(256),
    client_secret varchar(512),
    authorization_endpoint varchar(512),
    token_endpoint varchar(512),
    user_info_endpoint varchar(512),
    scopes varchar(256) DEFAULT 'openid profile email'::character varying,
    user_id_field varchar(128) DEFAULT 'sub'::character varying,
    username_field varchar(128) DEFAULT 'preferred_username'::character varying,
    display_name_field varchar(128) DEFAULT 'name'::character varying,
    email_field varchar(128) DEFAULT 'email'::character varying,
    well_known varchar(512),
    auth_style bigint DEFAULT 0,
    access_policy text,
    access_denied_message varchar(512),
    created_at timestamptz,
    updated_at timestamptz,
    CONSTRAINT custom_oauth_providers_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_custom_oauth_providers_slug ON custom_oauth_providers (slug);

CREATE TABLE external_identity_claims (
    id bigserial NOT NULL,
    provider varchar(32) NOT NULL,
    subject varchar(128) NOT NULL,
    user_id bigint NOT NULL,
    created_at timestamptz,
    CONSTRAINT external_identity_claims_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_external_identity_claims_user_id ON external_identity_claims (user_id);
CREATE UNIQUE INDEX idx_external_identity_subject ON external_identity_claims (provider, subject);
CREATE UNIQUE INDEX idx_external_identity_user ON external_identity_claims (provider, user_id);

CREATE TABLE login_encryption_keys (
    id bigserial NOT NULL,
    slot varchar(32) NOT NULL,
    private_key_pem text NOT NULL,
    CONSTRAINT login_encryption_keys_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_login_encryption_keys_slot ON login_encryption_keys (slot);

CREATE TABLE midjourneys (
    id bigserial NOT NULL,
    code bigint,
    user_id bigint,
    action varchar(40),
    mj_id text,
    prompt text,
    prompt_en text,
    description text,
    state text,
    submit_time bigint,
    start_time bigint,
    finish_time bigint,
    image_url text,
    video_url text,
    video_urls text,
    status varchar(20),
    progress varchar(30),
    fail_reason text,
    channel_id bigint,
    quota bigint,
    buttons text,
    properties text,
    token_id bigint DEFAULT 0,
    billing_channel_id bigint DEFAULT 0,
    CONSTRAINT midjourneys_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_midjourneys_action ON midjourneys (action);
CREATE INDEX idx_midjourneys_finish_time ON midjourneys (finish_time);
CREATE INDEX idx_midjourneys_mj_id ON midjourneys (mj_id);
CREATE INDEX idx_midjourneys_progress ON midjourneys (progress);
CREATE INDEX idx_midjourneys_start_time ON midjourneys (start_time);
CREATE INDEX idx_midjourneys_status ON midjourneys (status);
CREATE INDEX idx_midjourneys_submit_time ON midjourneys (submit_time);
CREATE INDEX idx_midjourneys_user_id ON midjourneys (user_id);

CREATE TABLE models (
    id bigserial NOT NULL,
    model_name varchar(128) NOT NULL,
    description text,
    icon varchar(128),
    tags varchar(255),
    vendor_id bigint,
    endpoints text,
    status bigint DEFAULT 1,
    sync_official bigint DEFAULT 1,
    created_time bigint,
    updated_time bigint,
    deleted_at timestamptz,
    name_rule bigint DEFAULT 0,
    CONSTRAINT models_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_models_deleted_at ON models (deleted_at);
CREATE INDEX idx_models_vendor_id ON models (vendor_id);
CREATE UNIQUE INDEX uk_model_name_delete_at ON models (model_name, deleted_at);

CREATE TABLE options (
    key text NOT NULL,
    value text,
    CONSTRAINT options_pkey PRIMARY KEY (key)
);

CREATE TABLE passkey_credentials (
    id bigserial NOT NULL,
    user_id bigint NOT NULL,
    rp_id varchar(253),
    credential_id varchar(512) NOT NULL,
    public_key text NOT NULL,
    attestation_type varchar(255),
    aa_guid varchar(512),
    sign_count bigint DEFAULT 0,
    clone_warning boolean,
    user_present boolean,
    user_verified boolean,
    backup_eligible boolean,
    backup_state boolean,
    transports text,
    attachment varchar(32),
    last_used_at timestamptz,
    created_at timestamptz,
    updated_at timestamptz,
    deleted_at timestamptz,
    CONSTRAINT passkey_credentials_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_passkey_credentials_credential_id ON passkey_credentials (credential_id);
CREATE INDEX idx_passkey_credentials_deleted_at ON passkey_credentials (deleted_at);
CREATE UNIQUE INDEX idx_passkey_credentials_user_id ON passkey_credentials (user_id);

CREATE TABLE payment_exchange_rates (
    from_currency varchar(8) NOT NULL,
    to_currency varchar(8) NOT NULL,
    rate varchar(64) NOT NULL,
    source varchar(32) DEFAULT ''::character varying NOT NULL,
    fetched_at bigint DEFAULT 0 NOT NULL,
    CONSTRAINT payment_exchange_rates_pkey PRIMARY KEY (from_currency, to_currency)
);

CREATE TABLE payment_methods (
    id bigserial NOT NULL,
    name varchar(100) DEFAULT ''::character varying NOT NULL,
    platform varchar(32) NOT NULL,
    icon varchar(255) DEFAULT ''::character varying NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    sort bigint DEFAULT 0 NOT NULL,
    enabled boolean NOT NULL,
    domain varchar(255) DEFAULT ''::character varying NOT NULL,
    config text DEFAULT ''::text NOT NULL,
    fee_mode bigint DEFAULT 0 NOT NULL,
    fee_rate_bp bigint DEFAULT 0 NOT NULL,
    fee_fixed bigint DEFAULT 0 NOT NULL,
    token varchar(64) NOT NULL,
    created_at bigint,
    updated_at bigint,
    deleted_at timestamptz,
    CONSTRAINT payment_methods_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_payment_methods_deleted_at ON payment_methods (deleted_at);
CREATE UNIQUE INDEX idx_payment_methods_token ON payment_methods (token);

CREATE TABLE payment_orders (
    id bigserial NOT NULL,
    order_no varchar(64) NOT NULL,
    user_id bigint NOT NULL,
    purpose varchar(16) NOT NULL,
    plan_id bigint DEFAULT 0 NOT NULL,
    subject varchar(255) DEFAULT ''::character varying NOT NULL,
    topup_amount bigint DEFAULT 0 NOT NULL,
    quota_amount bigint DEFAULT 0 NOT NULL,
    quota_rate varchar(64) DEFAULT ''::character varying NOT NULL,
    currency varchar(8) NOT NULL,
    price bigint DEFAULT 0 NOT NULL,
    adjustment bigint DEFAULT 0 NOT NULL,
    fee_amount bigint DEFAULT 0 NOT NULL,
    amount bigint DEFAULT 0 NOT NULL,
    payment_method_id bigint NOT NULL,
    platform varchar(32) NOT NULL,
    payment_amount bigint DEFAULT 0 NOT NULL,
    payment_currency varchar(8) DEFAULT ''::character varying NOT NULL,
    exchange_rate varchar(64) DEFAULT ''::character varying NOT NULL,
    trade_no varchar(255) DEFAULT ''::character varying NOT NULL,
    status bigint NOT NULL,
    fulfil_error text DEFAULT ''::text NOT NULL,
    user_subscription_id bigint DEFAULT 0 NOT NULL,
    client_ip varchar(64) DEFAULT ''::character varying NOT NULL,
    close_reason varchar(32) DEFAULT ''::character varying NOT NULL,
    created_at bigint,
    expires_at bigint DEFAULT 0 NOT NULL,
    paid_at bigint DEFAULT 0 NOT NULL,
    finished_at bigint DEFAULT 0 NOT NULL,
    closed_at bigint DEFAULT 0 NOT NULL,
    next_check_at bigint DEFAULT 0 NOT NULL,
    check_attempts bigint DEFAULT 0 NOT NULL,
    CONSTRAINT payment_orders_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_payment_orders_method_status ON payment_orders (payment_method_id, status);
CREATE UNIQUE INDEX idx_payment_orders_order_no ON payment_orders (order_no);
CREATE UNIQUE INDEX idx_payment_orders_platform_trade ON payment_orders (platform, trade_no) WHERE ((trade_no)::text <> ''::text);
CREATE INDEX idx_payment_orders_status_check ON payment_orders (status, next_check_at);
CREATE INDEX idx_payment_orders_status_paid ON payment_orders (status, paid_at);
CREATE INDEX idx_payment_orders_user ON payment_orders (user_id, id);

CREATE TABLE payment_unmatched (
    id bigserial NOT NULL,
    platform varchar(32) NOT NULL,
    trade_no varchar(255) NOT NULL,
    order_no varchar(64) NOT NULL,
    payment_method_id bigint DEFAULT 0 NOT NULL,
    user_id bigint DEFAULT 0 NOT NULL,
    amount bigint DEFAULT 0 NOT NULL,
    currency varchar(8) DEFAULT ''::character varying NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    created_at bigint,
    resolved_at bigint DEFAULT 0 NOT NULL,
    resolved_by bigint DEFAULT 0 NOT NULL,
    resolve_note text DEFAULT ''::text NOT NULL,
    CONSTRAINT payment_unmatched_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_payment_unmatched_payment ON payment_unmatched (platform, trade_no, order_no);
CREATE INDEX idx_payment_unmatched_resolved_at ON payment_unmatched (resolved_at);

CREATE TABLE perf_metrics (
    id bigserial NOT NULL,
    model_name varchar(128),
    "group" varchar(64),
    bucket_ts bigint,
    request_count bigint DEFAULT 0,
    success_count bigint DEFAULT 0,
    total_latency_ms bigint DEFAULT 0,
    ttft_sum_ms bigint DEFAULT 0,
    ttft_count bigint DEFAULT 0,
    output_tokens bigint DEFAULT 0,
    generation_ms bigint DEFAULT 0,
    CONSTRAINT perf_metrics_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_perf_bucket_ts ON perf_metrics (bucket_ts);
CREATE UNIQUE INDEX idx_perf_model_group_bucket ON perf_metrics (model_name, "group", bucket_ts);

CREATE TABLE prefill_groups (
    id bigserial NOT NULL,
    name varchar(64) NOT NULL,
    type varchar(32) NOT NULL,
    items json,
    description varchar(255),
    created_time bigint,
    updated_time bigint,
    deleted_at timestamptz,
    CONSTRAINT prefill_groups_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_prefill_groups_deleted_at ON prefill_groups (deleted_at);
CREATE INDEX idx_prefill_groups_type ON prefill_groups (type);
CREATE UNIQUE INDEX uk_prefill_name ON prefill_groups (name) WHERE (deleted_at IS NULL);

CREATE TABLE quota_data (
    id bigserial NOT NULL,
    user_id bigint,
    username varchar(64) DEFAULT ''::character varying,
    model_name varchar(64) DEFAULT ''::character varying,
    created_at bigint,
    use_group varchar(64) DEFAULT ''::character varying,
    token_id bigint DEFAULT 0,
    channel_id bigint DEFAULT 0,
    node_name varchar(64) DEFAULT ''::character varying,
    token_used bigint DEFAULT 0,
    count bigint DEFAULT 0,
    quota bigint DEFAULT 0,
    cache_read_tokens bigint DEFAULT 0,
    cache_creation_tokens bigint DEFAULT 0,
    CONSTRAINT quota_data_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_qdt_created_at ON quota_data (created_at);
CREATE INDEX idx_qdt_model_user_name ON quota_data (model_name, username);
CREATE INDEX idx_quota_data_channel_id ON quota_data (channel_id);
CREATE INDEX idx_quota_data_node_name ON quota_data (node_name);
CREATE INDEX idx_quota_data_token_id ON quota_data (token_id);
CREATE INDEX idx_quota_data_use_group ON quota_data (use_group);
CREATE INDEX idx_quota_data_user_id ON quota_data (user_id);
CREATE UNIQUE INDEX uk_quota_data_key ON quota_data (user_id, username, model_name, created_at, use_group, token_id, channel_id, node_name);

CREATE TABLE redemptions (
    id bigserial NOT NULL,
    user_id bigint,
    key character(32),
    status bigint DEFAULT 1,
    name text,
    quota bigint DEFAULT 100,
    created_time bigint,
    redeemed_time bigint,
    used_user_id bigint,
    deleted_at timestamptz,
    expired_time bigint,
    CONSTRAINT redemptions_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_redemptions_deleted_at ON redemptions (deleted_at);
CREATE UNIQUE INDEX idx_redemptions_key ON redemptions (key);
CREATE INDEX idx_redemptions_name ON redemptions (name);

CREATE TABLE setups (
    id bigserial NOT NULL,
    version varchar(50) NOT NULL,
    initialized_at bigint NOT NULL,
    CONSTRAINT setups_pkey PRIMARY KEY (id)
);

CREATE TABLE subscription_plans (
    id bigserial NOT NULL,
    title varchar(128) NOT NULL,
    subtitle varchar(255) DEFAULT ''::character varying,
    price_amount numeric(10,6) DEFAULT 0 NOT NULL,
    duration_unit varchar(16) DEFAULT 'month'::character varying NOT NULL,
    duration_value bigint DEFAULT 1 NOT NULL,
    custom_seconds bigint DEFAULT 0 NOT NULL,
    enabled boolean DEFAULT true,
    sort_order bigint DEFAULT 0,
    allow_balance_pay boolean,
    allow_wallet_overflow boolean,
    max_purchase_per_user bigint DEFAULT 0,
    upgrade_group varchar(64) DEFAULT ''::character varying,
    downgrade_group varchar(64) DEFAULT ''::character varying,
    total_amount bigint DEFAULT 0 NOT NULL,
    quota_reset_period varchar(16) DEFAULT 'never'::character varying,
    quota_reset_custom_seconds bigint DEFAULT 0,
    created_at bigint,
    updated_at bigint,
    CONSTRAINT subscription_plans_pkey PRIMARY KEY (id)
);

CREATE TABLE subscription_pre_consume_records (
    id bigserial NOT NULL,
    request_id varchar(64),
    user_id bigint,
    user_subscription_id bigint,
    pre_consumed bigint DEFAULT 0 NOT NULL,
    status varchar(32),
    created_at bigint,
    updated_at bigint,
    CONSTRAINT subscription_pre_consume_records_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_subscription_pre_consume_records_request_id ON subscription_pre_consume_records (request_id);
CREATE INDEX idx_subscription_pre_consume_records_status ON subscription_pre_consume_records (status);
CREATE INDEX idx_subscription_pre_consume_records_updated_at ON subscription_pre_consume_records (updated_at);
CREATE INDEX idx_subscription_pre_consume_records_user_id ON subscription_pre_consume_records (user_id);
CREATE INDEX idx_subscription_pre_consume_records_user_subscription_id ON subscription_pre_consume_records (user_subscription_id);

CREATE TABLE system_instances (
    node_name varchar(128) NOT NULL,
    info text,
    started_at bigint,
    last_seen_at bigint,
    created_at bigint,
    updated_at bigint,
    CONSTRAINT system_instances_pkey PRIMARY KEY (node_name)
);
CREATE INDEX idx_system_instances_created_at ON system_instances (created_at);
CREATE INDEX idx_system_instances_last_seen_at ON system_instances (last_seen_at);
CREATE INDEX idx_system_instances_started_at ON system_instances (started_at);
CREATE INDEX idx_system_instances_updated_at ON system_instances (updated_at);

CREATE TABLE system_task_locks (
    type varchar(64) NOT NULL,
    task_id varchar(64),
    locked_by varchar(128),
    locked_until bigint,
    updated_at bigint,
    CONSTRAINT system_task_locks_pkey PRIMARY KEY (type)
);
CREATE INDEX idx_system_task_locks_locked_by ON system_task_locks (locked_by);
CREATE INDEX idx_system_task_locks_locked_until ON system_task_locks (locked_until);
CREATE INDEX idx_system_task_locks_task_id ON system_task_locks (task_id);
CREATE INDEX idx_system_task_locks_updated_at ON system_task_locks (updated_at);

CREATE TABLE system_tasks (
    id bigserial NOT NULL,
    task_id varchar(64),
    type varchar(64),
    status varchar(32),
    active_key varchar(64),
    payload text,
    state text,
    result text,
    error text,
    locked_by varchar(128),
    created_at bigint,
    updated_at bigint,
    CONSTRAINT system_tasks_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_system_tasks_active_key ON system_tasks (active_key);
CREATE INDEX idx_system_tasks_created_at ON system_tasks (created_at);
CREATE INDEX idx_system_tasks_locked_by ON system_tasks (locked_by);
CREATE INDEX idx_system_tasks_status ON system_tasks (status);
CREATE UNIQUE INDEX idx_system_tasks_task_id ON system_tasks (task_id);
CREATE INDEX idx_system_tasks_type ON system_tasks (type);
CREATE INDEX idx_system_tasks_updated_at ON system_tasks (updated_at);

CREATE TABLE task_plugins (
    id bigserial NOT NULL,
    key varchar(128) NOT NULL,
    api_version bigint NOT NULL,
    version varchar(64) NOT NULL,
    source text NOT NULL,
    source_hash varchar(64) NOT NULL,
    icon text,
    enabled boolean NOT NULL,
    active boolean NOT NULL,
    created_at bigint NOT NULL,
    remark text,
    CONSTRAINT task_plugins_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_task_plugins_active ON task_plugins (active);
CREATE UNIQUE INDEX uk_task_plugin_key_version ON task_plugins (key, version);

CREATE TABLE tasks (
    id bigserial NOT NULL,
    created_at bigint,
    updated_at bigint,
    task_id varchar(191),
    platform varchar(30),
    user_id bigint,
    "group" varchar(50),
    channel_id bigint,
    quota bigint,
    action varchar(40),
    status varchar(20),
    fail_reason text,
    submit_time bigint,
    start_time bigint,
    finish_time bigint,
    progress varchar(20),
    properties json,
    private_data json,
    data json,
    CONSTRAINT tasks_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_tasks_action ON tasks (action);
CREATE INDEX idx_tasks_channel_id ON tasks (channel_id);
CREATE INDEX idx_tasks_created_at ON tasks (created_at);
CREATE INDEX idx_tasks_finish_time ON tasks (finish_time);
CREATE INDEX idx_tasks_platform ON tasks (platform);
CREATE INDEX idx_tasks_progress ON tasks (progress);
CREATE INDEX idx_tasks_start_time ON tasks (start_time);
CREATE INDEX idx_tasks_status ON tasks (status);
CREATE INDEX idx_tasks_submit_time ON tasks (submit_time);
CREATE INDEX idx_tasks_task_id ON tasks (task_id);
CREATE INDEX idx_tasks_user_id ON tasks (user_id);

CREATE TABLE tokens (
    id bigserial NOT NULL,
    user_id bigint,
    key varchar(128),
    status bigint DEFAULT 1,
    name text,
    created_time bigint,
    accessed_time bigint,
    expired_time bigint DEFAULT '-1'::integer,
    remain_quota bigint DEFAULT 0,
    unlimited_quota boolean,
    model_limits_enabled boolean,
    model_limits text,
    allow_ips text DEFAULT ''::text,
    used_quota bigint DEFAULT 0,
    "group" text DEFAULT ''::text,
    cross_group_retry boolean,
    auto_groups text,
    deleted_at timestamptz,
    allow_user_quota_query boolean,
    CONSTRAINT tokens_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_tokens_deleted_at ON tokens (deleted_at);
CREATE UNIQUE INDEX idx_tokens_key ON tokens (key);
CREATE INDEX idx_tokens_name ON tokens (name);
CREATE INDEX idx_tokens_user_id ON tokens (user_id);

CREATE TABLE two_fa_backup_codes (
    id bigserial NOT NULL,
    user_id bigint NOT NULL,
    code_hash varchar(255) NOT NULL,
    is_used boolean,
    used_at timestamptz,
    created_at timestamptz,
    deleted_at timestamptz,
    CONSTRAINT two_fa_backup_codes_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_two_fa_backup_codes_deleted_at ON two_fa_backup_codes (deleted_at);
CREATE INDEX idx_two_fa_backup_codes_user_id ON two_fa_backup_codes (user_id);

CREATE TABLE two_fas (
    id bigserial NOT NULL,
    user_id bigint NOT NULL,
    secret varchar(255) NOT NULL,
    is_enabled boolean,
    failed_attempts bigint DEFAULT 0,
    locked_until timestamptz,
    last_used_at timestamptz,
    created_at timestamptz,
    updated_at timestamptz,
    deleted_at timestamptz,
    CONSTRAINT two_fas_pkey PRIMARY KEY (id),
    CONSTRAINT uni_two_fas_user_id UNIQUE (user_id)
);
CREATE INDEX idx_two_fas_deleted_at ON two_fas (deleted_at);
CREATE INDEX idx_two_fas_user_id ON two_fas (user_id);

CREATE TABLE user_access_tokens (
    id bigserial NOT NULL,
    user_id bigint,
    name varchar(64),
    token_hash varchar(64),
    token_hint varchar(8),
    scopes varchar(2048),
    expires_at bigint,
    last_used_at bigint,
    last_used_ip varchar(64),
    created_at bigint,
    CONSTRAINT user_access_tokens_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_user_access_tokens_expires_at ON user_access_tokens (expires_at);
CREATE UNIQUE INDEX idx_user_access_tokens_token_hash ON user_access_tokens (token_hash);
CREATE INDEX idx_user_access_tokens_user_id ON user_access_tokens (user_id);

CREATE TABLE user_oauth_bindings (
    id bigserial NOT NULL,
    user_id bigint NOT NULL,
    provider_id bigint NOT NULL,
    provider_user_id varchar(256) NOT NULL,
    created_at timestamptz,
    CONSTRAINT user_oauth_bindings_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX ux_provider_userid ON user_oauth_bindings (provider_id, provider_user_id);
CREATE UNIQUE INDEX ux_user_provider ON user_oauth_bindings (user_id, provider_id);

CREATE TABLE user_sessions (
    sid varchar(64) NOT NULL,
    user_id bigint NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    user_auth_version bigint NOT NULL,
    status varchar(16) NOT NULL,
    refresh_hash character(64) NOT NULL,
    previous_refresh_hash varchar(64),
    previous_valid_until bigint DEFAULT 0 NOT NULL,
    login_method varchar(32) NOT NULL,
    ip varchar(64),
    user_agent text,
    created_at bigint,
    last_active_at bigint NOT NULL,
    expires_at bigint NOT NULL,
    revoked_at bigint DEFAULT 0 NOT NULL,
    revoked_reason varchar(64),
    CONSTRAINT user_sessions_pkey PRIMARY KEY (sid)
);
CREATE INDEX idx_user_sessions_expires_at ON user_sessions (expires_at);
CREATE INDEX idx_user_sessions_status_revoked ON user_sessions (status, revoked_at);
CREATE INDEX idx_user_sessions_user_created ON user_sessions (user_id, created_at);
CREATE INDEX idx_user_sessions_user_status_expiry ON user_sessions (user_id, status, expires_at);

CREATE TABLE user_subscriptions (
    id bigserial NOT NULL,
    user_id bigint,
    plan_id bigint,
    amount_total bigint DEFAULT 0 NOT NULL,
    amount_used bigint DEFAULT 0 NOT NULL,
    start_time bigint,
    end_time bigint,
    status varchar(32),
    source varchar(32) DEFAULT 'order'::character varying,
    last_reset_time bigint DEFAULT 0,
    next_reset_time bigint DEFAULT 0,
    upgrade_group varchar(64) DEFAULT ''::character varying,
    prev_user_group varchar(64) DEFAULT ''::character varying,
    downgrade_group varchar(64) DEFAULT ''::character varying,
    allow_wallet_overflow boolean,
    created_at bigint,
    updated_at bigint,
    CONSTRAINT user_subscriptions_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_user_sub_active ON user_subscriptions (user_id, status, end_time);
CREATE INDEX idx_user_subscriptions_end_time ON user_subscriptions (end_time);
CREATE INDEX idx_user_subscriptions_next_reset_time ON user_subscriptions (next_reset_time);
CREATE INDEX idx_user_subscriptions_plan_id ON user_subscriptions (plan_id);
CREATE INDEX idx_user_subscriptions_status ON user_subscriptions (status);
CREATE INDEX idx_user_subscriptions_user_id ON user_subscriptions (user_id);

CREATE TABLE users (
    id bigserial NOT NULL,
    username text,
    password text NOT NULL,
    display_name text,
    role bigint DEFAULT 1,
    status bigint DEFAULT 1,
    email text,
    github_id text,
    discord_id text,
    oidc_id text,
    wechat_id text,
    telegram_id text,
    access_token character(32),
    access_token_created_at bigint,
    quota bigint DEFAULT 0,
    used_quota bigint DEFAULT 0,
    request_count bigint DEFAULT 0,
    "group" varchar(64) DEFAULT 'default'::character varying,
    aff_code varchar(32),
    aff_count bigint DEFAULT 0,
    aff_quota bigint DEFAULT 0,
    aff_history bigint DEFAULT 0,
    inviter_id bigint,
    deleted_at timestamptz,
    linux_do_id text,
    setting text,
    remark varchar(255),
    created_at bigint,
    last_login_at bigint DEFAULT 0,
    auth_version bigint DEFAULT 1 NOT NULL,
    CONSTRAINT uni_users_username UNIQUE (username),
    CONSTRAINT users_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_users_access_token ON users (access_token);
CREATE UNIQUE INDEX idx_users_aff_code ON users (aff_code);
CREATE INDEX idx_users_created_at ON users (created_at);
CREATE INDEX idx_users_deleted_at ON users (deleted_at);
CREATE INDEX idx_users_discord_id ON users (discord_id);
CREATE INDEX idx_users_display_name ON users (display_name);
CREATE INDEX idx_users_email ON users (email);
CREATE INDEX idx_users_git_hub_id ON users (github_id);
CREATE INDEX idx_users_inviter_id ON users (inviter_id);
CREATE INDEX idx_users_linux_do_id ON users (linux_do_id);
CREATE INDEX idx_users_oidc_id ON users (oidc_id);
CREATE INDEX idx_users_telegram_id ON users (telegram_id);
CREATE INDEX idx_users_username ON users (username);
CREATE INDEX idx_users_we_chat_id ON users (wechat_id);

CREATE TABLE vendors (
    id bigserial NOT NULL,
    name varchar(128) NOT NULL,
    description text,
    icon varchar(128),
    status bigint DEFAULT 1,
    created_time bigint,
    updated_time bigint,
    deleted_at timestamptz,
    CONSTRAINT vendors_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_vendors_deleted_at ON vendors (deleted_at);
CREATE UNIQUE INDEX uk_vendor_name_delete_at ON vendors (name, deleted_at);
