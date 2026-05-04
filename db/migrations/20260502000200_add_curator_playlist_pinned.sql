-- +goose Up
alter table playlist add column curator_pinned bool default false not null;

create index if not exists playlist_curator_home
    on playlist (owner_id, public, curator_pinned, updated_at);

-- +goose Down
drop index if exists playlist_curator_home;

