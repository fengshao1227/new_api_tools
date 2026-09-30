import test from 'node:test'
import assert from 'node:assert/strict'
import {
  createInsightsCSV, csvCell, formatMetric, formatUSD, formatUTCTime,
  insightsQuery, insightsWarnings, resolveInsightsWindow, userIdFromSearch,
} from '../src/lib/user-insights.ts'

const now = Date.parse('2026-09-30T12:34:56Z') / 1000
const draft = { preset: '30', start: '', end: '', model: '' }

test('expected missing reasoning tokens do not trigger a partial-data warning', () => {
  assert.deepEqual(insightsWarnings(['reasoning_tokens_not_recorded']), [])
  assert.deepEqual(insightsWarnings(null), [])
})

test('availability warnings are readable, deduplicated and never expose internal codes', () => {
  assert.deepEqual(insightsWarnings(['logs_unavailable', 'logs_unavailable', 'reasoning_tokens_not_recorded']), ['调用日志不可用，暂时无法统计模型用量。'])
  assert.deepEqual(insightsWarnings(['new_internal_code']), ['部分数据暂不可用，请检查服务配置。'])
  for (const code of ['acquisition_schema_missing', 'account_times_incomplete', 'oauth_bindings_unavailable', 'risk_unavailable', 'payments_unavailable', 'credited_amount_incomplete', 'tasks_unavailable', 'token_details_unavailable', 'log_token_details_unavailable', 'log_database_fallback_may_be_stale']) {
    const [label] = insightsWarnings([code])
    assert.ok(label && !label.includes(code) && !label.includes('请检查服务配置'), code)
  }
})

test('rolling ranges are exact UTC seconds, not local calendar dates', () => {
  for (const days of ['7', '30', '90']) {
    const range = resolveInsightsWindow({ ...draft, preset: days }, now)
    assert.equal(range.end_time, now)
    assert.equal(range.end_time - range.start_time, Number(days) * 86400)
  }
})

test('all retained records uses epoch zero and a fixed exclusive end', () => {
  const range = resolveInsightsWindow({ ...draft, preset: 'all' }, now)
  assert.equal(range.start_time, 0)
  assert.equal(range.all_time, true)
  assert.equal(insightsQuery(range).get('end_time'), String(now))
})

test('custom datetime inputs are UTC even on a non-UTC computer', () => {
  const range = resolveInsightsWindow({ ...draft, preset: 'custom', start: '2026-09-29T12:34', end: '2026-09-30T12:34:56' })
  assert.equal(range.start_time, Date.parse('2026-09-29T12:34:00Z') / 1000)
  assert.equal(range.end_time, now)
})

test('invalid calendar dates, missing values and reversed windows are rejected', () => {
  for (const [start, end] of [
    ['2026-02-30T00:00', '2026-03-01T00:00'], ['', '2026-03-01T00:00'],
    ['2026-03-01T00:00', '2026-03-01T00:00'], ['2026-03-02T00:00', '2026-03-01T00:00'],
    ['1969-12-31T00:00', '2026-03-01T00:00'],
  ]) assert.throws(() => resolveInsightsWindow({ ...draft, preset: 'custom', start, end }))
})

test('model exact filter survives punctuation without becoming another query parameter', () => {
  const model = 'claude[1m]&x=y+中文'
  const query = insightsQuery(resolveInsightsWindow({ ...draft, model }, now))
  assert.equal(query.get('model'), model)
  assert.equal(query.has('x'), false)
  assert.equal(insightsQuery(resolveInsightsWindow(draft, now)).has('model'), false)
})

test('user links require a positive safe integer, not a prefix or exponent', () => {
  assert.equal(userIdFromSearch('?user_id=123'), 123)
  for (const value of ['', '0', '-1', '1e3', '12text', '1.2', '999999999999999999']) {
    assert.equal(userIdFromSearch(`?user_id=${value}`), null)
  }
})

test('unknown values remain unknown while actual zero remains zero', () => {
  assert.equal(formatMetric(null), '未记录')
  assert.equal(formatMetric(undefined), '未记录')
  assert.equal(formatMetric(0), '0')
  assert.equal(formatUSD(null), '未记录')
  assert.equal(formatUSD(0), '$0.00')
  assert.equal(formatUTCTime(null), '未记录')
  assert.equal(formatUTCTime(now), '2026-09-30 12:34:56 UTC')
})

test('CSV cells neutralize spreadsheet formulas including whitespace prefixes', () => {
  for (const value of ['=1+1', '+cmd', '-1+2', '@SUM(A1)', ' \t=HYPERLINK("x")', '\r\n+1', '\uFEFF=1']) {
    assert.ok(csvCell(value).startsWith('"\''), value)
  }
  assert.equal(csvCell('a,"b"'), '"a,""b"""')
  assert.equal(csvCell(null), '"未记录"')
})

test('CSV exports current identity and window, counts input/output once and preserves unknown cache', () => {
  const report = {
    profile: { id: 42, username: '=FORMULA', email: 'user@example.test' },
    window: { start_time: now - 86400, end_time: now, model: 'chosen', all_time: false },
    models: [{ model_name: 'chosen', billing_records: 2, error_records: 1, refund_records: 1,
      input_tokens: 100, output_tokens: 20, cache_read_tokens: 30, cache_write_tokens: null,
      reasoning_tokens: null, charged_usd: 4, refund_usd: 1, last_record_at: now - 1 }],
  }
  const csv = createInsightsCSV(report)
  assert.ok(csv.startsWith('\uFEFF'))
  assert.ok(csv.includes('"\'=FORMULA"'))
  assert.ok(csv.includes('"user@example.test"'))
  assert.ok(csv.includes('"100","20","120","30","未记录","未记录","4","1"'))
  assert.ok(csv.includes('2026-09-29 12:34:56 UTC'))
  assert.equal(csv.split('\r\n').length, 2)
})
