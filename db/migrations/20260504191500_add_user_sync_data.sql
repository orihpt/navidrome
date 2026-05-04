-- +goose Up
alter table user add column user_data text;
alter table user add column user_data_updated_at datetime;

-- +goose Down

