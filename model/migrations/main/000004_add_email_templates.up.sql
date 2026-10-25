-- add_email_templates
--
-- Administrators can replace the built-in template of an email in each
-- backend language. A row is such a replacement; an email without one in
-- the reader's language keeps its built-in template.

CREATE TABLE email_templates (
    id bigserial NOT NULL,
    kind varchar(32) NOT NULL,
    language varchar(16) NOT NULL,
    subject text NOT NULL,
    body text NOT NULL,
    updated_at bigint,
    CONSTRAINT email_templates_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_email_templates_kind_language ON email_templates (kind, language);
