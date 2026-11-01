module example.com/kubron-ext-authenticator

go 1.24

require github.com/24DSO-labs/kubron/pkg/ext v0.0.0

// Внешний модуль зависит только от публичного пакета расширений, а не от всего ядра.
replace github.com/24DSO-labs/kubron/pkg/ext => ../../pkg/ext
