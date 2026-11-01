-- Откат стирает репозитории Helm (вместе с зашифрованными паролями) и журнал операций.
DROP TABLE IF EXISTS helm_operations;
DROP TABLE IF EXISTS helm_repositories;
