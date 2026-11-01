-- Опубликованный адрес Argo CD кластера: если задан, OpenArgoUI открывает его вместо port-forward.
ALTER TABLE clusters ADD COLUMN argo_url TEXT NOT NULL DEFAULT '';
