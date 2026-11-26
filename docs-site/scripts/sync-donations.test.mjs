// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0
import test from 'node:test'
import assert from 'node:assert/strict'
import { decimalFromUnits } from './sync-donations.mjs'
test('precisely formats BTC, ETH and token unit amounts', () => {
  assert.equal(decimalFromUnits('123456789', 8), '1.23456789')
  assert.equal(decimalFromUnits('100000000', 8), '1')
  assert.equal(decimalFromUnits('1', 8), '0.00000001')
  assert.equal(decimalFromUnits('1234567', 6), '1.234567')
  assert.equal(decimalFromUnits('1000000', 6), '1')
  assert.equal(decimalFromUnits('42', 6), '0.000042')
  assert.equal(decimalFromUnits('123456789012345678901234', 18), '123456.789012345678901234')
})
