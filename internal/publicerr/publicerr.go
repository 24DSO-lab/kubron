// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package publicerr помечает ошибки, текст которых безопасно и полезно показать пользователю как есть.
//
// Веб-сервер заменяет текст внутренних ошибок (БД, сеть, TLS, файлы) общим сообщением с идентификатором запроса:
// в тексте могут быть внутренние адреса, имена таблиц и строки подключения. Ошибку, которую пользователю нужно
// прочитать (например, составленную из проверки его ввода поверх сетевой ошибки), помечают Wrap: тогда сервер отдаёт
// её сообщение без изменений. Маркер не нужен для сообщений Kubernetes API и ошибок проверки ввода без вложенных
// ошибок БД и сети: они остаются как есть и без него. Пакет без зависимостей, чтобы им мог пользоваться любой слой.
package publicerr

import "errors"

type publicError struct{ err error }

func (e *publicError) Error() string { return e.err.Error() }
func (e *publicError) Unwrap() error { return e.err }

// Wrap помечает ошибку публичной; Wrap(nil) возвращает nil. Текст ошибки не меняется. Помеченную ошибку нельзя
// строить из сообщения внутренней ошибки (err.Error() сетевой или БД): текст целиком уйдёт пользователю.
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	var already *publicError
	if errors.As(err, &already) {
		return err
	}
	return &publicError{err: err}
}

// New создаёт публичную ошибку с заданным текстом.
func New(text string) error { return &publicError{err: errors.New(text)} }

// Is сообщает, что в цепочке err есть метка Wrap.
func Is(err error) bool {
	var p *publicError
	return errors.As(err, &p)
}
