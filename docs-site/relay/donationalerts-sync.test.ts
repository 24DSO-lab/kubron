// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import test from 'node:test'
import assert from 'node:assert/strict'
import { parseDonationTimestamp } from './donationalerts-sync.ts'

test('parses ISO timestamps with UTC suffix without appending a second timezone', () => {
  const value = '2026-11-27T10:15:30.000Z'
  assert.equal(parseDonationTimestamp(value), Date.parse(value))
})

test('preserves explicit timezone offsets', () => {
  const value = '2026-11-27T13:15:30+03:00'
  assert.equal(parseDonationTimestamp(value), Date.parse(value))
})

test('treats DonationAlerts timestamps without a timezone as UTC', () => {
  const value = '2026-11-27 10:15:30'
  assert.equal(parseDonationTimestamp(value), Date.parse('2026-11-27T10:15:30Z'))
})

test('rejects missing and invalid timestamps', () => {
  assert.equal(parseDonationTimestamp(null), null)
  assert.equal(parseDonationTimestamp(''), null)
  assert.equal(parseDonationTimestamp('not-a-date'), null)
})
