-- +goose Up
-- Conversation history for the in-process Eino ReAct agent. Each message
-- persists as an Eino schema.Message JSON blob
-- ordered by row id; a turn rebuilds by reading the stream back, Flue-style.
-- Canonical household effects stay in the commands tables: this history owns
-- the conversation only.
CREATE TABLE agent_conversations(
    id         TEXT NOT NULL PRIMARY KEY CHECK (substr(id, 1, 6) = 'aconv_'),
    created_at TEXT NOT NULL
);

CREATE TABLE agent_messages(
    id              INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    role            TEXT NOT NULL CHECK (length(role) BETWEEN 1 AND 32),
    message_json    TEXT NOT NULL CHECK (json_valid(message_json)),
    created_at      TEXT NOT NULL
);

CREATE INDEX agent_messages_conversation_idx
    ON agent_messages(conversation_id, id);

-- +goose Down
DROP TABLE agent_messages;
DROP TABLE agent_conversations;
