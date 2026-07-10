/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  DEFAULT_CONFIG,
  DEFAULT_PARAMETER_ENABLED,
  MESSAGE_ROLES,
  MESSAGE_STATUS,
  STORAGE_KEYS,
} from '../../constants'
import type { PlaygroundConfig, ParameterEnabled, Message } from '../../types'
import {
  finalizeMessage,
  isAssistantMessagePending,
  sanitizeMessagesOnLoad,
} from '../message/message-streaming-utils'
import { completeAssistantTiming } from '../message/message-timing-utils'
import { hasMessageContent } from '../message/message-utils'
import {
  MAX_LOADED_MESSAGE_CHARS,
  MAX_LOADED_MESSAGES_CHARS,
  MAX_STORED_MESSAGES,
  MAX_STORED_MESSAGES_BYTES,
  STORAGE_VERSION,
  messagesSchema,
  parameterEnabledSchema,
  playgroundConfigSchema,
} from './storage-schema'

type StoredEnvelope<T> = {
  version: number
  data: T
}

type StorageLoadResult<T> = {
  data: T
  migrated: boolean
}

export type PlaygroundBackupData = {
  config: PlaygroundConfig
  messages: Message[]
  parameterEnabled: ParameterEnabled
}

export type PlaygroundBackupFile = PlaygroundBackupData & {
  exportedAt: string
  format: 'new-api-playground-backup'
  version: 1
}

const TRUNCATED_CONTENT_SUFFIX = '\n\n[...]'
const MIN_PREFIX_COLLAPSE_LENGTH = 2000
const MIN_REPEATED_SECTION_COUNT = 3
const SECTION_HEADING_LINE_PATTERN = /^#{2,6}\s+\d+\.\s+.+$/gm
const CONFIG_FIELD_NAMES = [
  'model',
  'group',
  'temperature',
  'top_p',
  'max_tokens',
  'frequency_penalty',
  'presence_penalty',
  'seed',
  'stream',
] as const
const PARAMETER_ENABLED_FIELD_NAMES = [
  'temperature',
  'top_p',
  'max_tokens',
  'frequency_penalty',
  'presence_penalty',
  'seed',
] as const

function readStoredValue(key: string): unknown | null {
  const saved = localStorage.getItem(key)
  if (!saved) return null

  return JSON.parse(saved) as unknown
}

function readStoredMessagesValue(
  key: string,
  enforceSizeLimit: boolean
): unknown | null {
  const saved = localStorage.getItem(key)
  if (!saved) return null

  if (enforceSizeLimit && saved.length > MAX_STORED_MESSAGES_BYTES) {
    return null
  }

  return JSON.parse(saved) as unknown
}

function unwrapStoredValue(value: unknown): unknown {
  if (!value || typeof value !== 'object') {
    return value
  }

  if ('version' in value && 'data' in value) {
    return (value as StoredEnvelope<unknown>).data
  }

  return value
}

function writeStoredValue<T>(key: string, data: T): void {
  const payload: StoredEnvelope<T> = {
    version: STORAGE_VERSION,
    data,
  }

  localStorage.setItem(key, JSON.stringify(payload))
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function getString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

function getFiniteNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value
  }

  if (typeof value !== 'string' || value.trim() === '') {
    return undefined
  }

  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : undefined
}

function getBoolean(value: unknown): boolean | undefined {
  return typeof value === 'boolean' ? value : undefined
}

function hasAnyProperty(
  value: unknown,
  propertyNames: readonly string[]
): boolean {
  return isRecord(value) && propertyNames.some((name) => name in value)
}

function isMessageRole(value: unknown): value is Message['from'] {
  return (
    value === MESSAGE_ROLES.USER ||
    value === MESSAGE_ROLES.ASSISTANT ||
    value === MESSAGE_ROLES.SYSTEM
  )
}

function getLegacyMessageStatus(value: unknown): Message['status'] {
  if (
    value === MESSAGE_STATUS.LOADING ||
    value === MESSAGE_STATUS.COMPLETE ||
    value === MESSAGE_STATUS.ERROR
  ) {
    return value
  }

  if (value === 'incomplete') {
    return MESSAGE_STATUS.STREAMING
  }

  return undefined
}

function getLegacyTextContent(content: unknown): string {
  if (typeof content === 'string') {
    return content
  }

  if (!Array.isArray(content)) {
    return ''
  }

  for (const item of content) {
    if (!isRecord(item) || item.type !== 'text') {
      continue
    }

    return getString(item.text) ?? ''
  }

  return ''
}

function getLegacyMessagesArray(value: unknown): unknown[] | null {
  if (Array.isArray(value)) {
    return value
  }

  if (!isRecord(value) || !Array.isArray(value.messages)) {
    return null
  }

  return value.messages
}

function convertLegacyMessage(value: unknown, index: number): Message | null {
  if (!isRecord(value) || !isMessageRole(value.role)) {
    return null
  }

  const rawId = getString(value.id) ?? getString(value.key)
  const createdAt =
    getFiniteNumber(value.createAt) ?? getFiniteNumber(value.createdAt)
  const content = getLegacyTextContent(value.content)
  const reasoningContent = getString(value.reasoningContent)?.trim()
  const status = getLegacyMessageStatus(value.status)

  const message: Message = {
    key: `legacy:${rawId ?? index}:${index}`,
    from: value.role,
    versions: [
      {
        id: `legacy:${rawId ?? index}`,
        content,
      },
    ],
    createdAt,
    status,
  }

  if (reasoningContent) {
    message.reasoning = {
      content: reasoningContent,
      duration: 0,
    }
  }

  const errorCode = getString(value.errorCode)
  if (errorCode) {
    message.errorCode = errorCode
  }

  return message
}

function convertLegacyMessages(value: unknown): Message[] | null {
  const legacyMessages = getLegacyMessagesArray(value)
  if (!legacyMessages) {
    return null
  }

  const convertedMessages = legacyMessages
    .map(convertLegacyMessage)
    .filter((message): message is Message => message !== null)

  if (convertedMessages.length === 0 && legacyMessages.length > 0) {
    return null
  }

  return convertedMessages
}

function readMessagesFromValue(value: unknown): Message[] | null {
  const unwrapped = unwrapStoredValue(value)
  const parsed = messagesSchema.safeParse(unwrapped)
  if (parsed.success) {
    return parsed.data as Message[]
  }

  const converted = convertLegacyMessages(unwrapped)
  if (!converted) {
    return null
  }

  const convertedParsed = messagesSchema.safeParse(converted)
  return convertedParsed.success ? (convertedParsed.data as Message[]) : null
}

function readMessagesFromKey(
  key: string,
  migrated: boolean,
  enforceSizeLimit: boolean
): StorageLoadResult<Message[]> | null {
  try {
    const saved = readStoredMessagesValue(key, enforceSizeLimit)
    if (!saved) return null

    const messages = readMessagesFromValue(saved)
    return messages ? { data: messages, migrated } : null
  } catch {
    return null
  }
}

function getInputsObject(value: unknown): Record<string, unknown> | null {
  if (!isRecord(value) || !isRecord(value.inputs)) {
    return null
  }

  return value.inputs
}

function convertLegacyConfig(value: unknown): Partial<PlaygroundConfig> | null {
  const inputs = getInputsObject(value)
  if (!inputs) {
    return null
  }

  const config: Partial<PlaygroundConfig> = {}
  const model = getString(inputs.model)
  const group = getString(inputs.group)
  const temperature = getFiniteNumber(inputs.temperature)
  const topP = getFiniteNumber(inputs.top_p)
  const maxTokens = getFiniteNumber(inputs.max_tokens)
  const frequencyPenalty = getFiniteNumber(inputs.frequency_penalty)
  const presencePenalty = getFiniteNumber(inputs.presence_penalty)
  const stream = getBoolean(inputs.stream)

  if (model !== undefined) config.model = model
  if (group !== undefined) config.group = group
  if (temperature !== undefined) config.temperature = temperature
  if (topP !== undefined) config.top_p = topP
  if (maxTokens !== undefined) config.max_tokens = maxTokens
  if (frequencyPenalty !== undefined) {
    config.frequency_penalty = frequencyPenalty
  }
  if (presencePenalty !== undefined) {
    config.presence_penalty = presencePenalty
  }
  if (inputs.seed === null) {
    config.seed = null
  } else {
    const seed = getFiniteNumber(inputs.seed)
    if (seed !== undefined) config.seed = seed
  }
  if (stream !== undefined) config.stream = stream

  return Object.keys(config).length > 0 ? config : null
}

function readConfigFromValue(value: unknown): Partial<PlaygroundConfig> | null {
  const unwrapped = unwrapStoredValue(value)
  const converted = convertLegacyConfig(unwrapped)
  if (converted) {
    const convertedParsed = playgroundConfigSchema.safeParse(converted)
    return convertedParsed.success ? convertedParsed.data : null
  }

  if (!hasAnyProperty(unwrapped, CONFIG_FIELD_NAMES)) {
    return null
  }

  const parsed = playgroundConfigSchema.safeParse(unwrapped)
  return parsed.success ? parsed.data : null
}

function readConfigFromKey(
  key: string,
  migrated: boolean
): StorageLoadResult<Partial<PlaygroundConfig>> | null {
  try {
    const saved = readStoredValue(key)
    if (!saved) return null

    const config = readConfigFromValue(saved)
    return config ? { data: config, migrated } : null
  } catch {
    return null
  }
}

function convertLegacyParameterEnabled(
  value: unknown
): Partial<ParameterEnabled> | null {
  const source =
    isRecord(value) && isRecord(value.parameterEnabled)
      ? value.parameterEnabled
      : value

  const parsed = parameterEnabledSchema.safeParse(source)
  return parsed.success ? parsed.data : null
}

function readParameterEnabledFromValue(
  value: unknown
): Partial<ParameterEnabled> | null {
  const unwrapped = unwrapStoredValue(value)
  const converted = convertLegacyParameterEnabled(unwrapped)
  if (converted && Object.keys(converted).length > 0) {
    return converted
  }

  if (!hasAnyProperty(unwrapped, PARAMETER_ENABLED_FIELD_NAMES)) {
    return null
  }

  const parsed = parameterEnabledSchema.safeParse(unwrapped)
  return parsed.success ? parsed.data : null
}

function readParameterEnabledFromKey(
  key: string,
  migrated: boolean
): StorageLoadResult<Partial<ParameterEnabled>> | null {
  try {
    const saved = readStoredValue(key)
    if (!saved) return null

    const parameterEnabled = readParameterEnabledFromValue(saved)
    return parameterEnabled ? { data: parameterEnabled, migrated } : null
  } catch {
    return null
  }
}

function trimMessages(messages: Message[]): Message[] {
  if (messages.length <= MAX_STORED_MESSAGES) {
    return messages
  }

  return messages.slice(-MAX_STORED_MESSAGES)
}

function getMessageSize(message: Message): number {
  const versionsSize = message.versions.reduce(
    (total, version) => total + version.content.length,
    0
  )
  const reasoningSize = message.reasoning?.content.length ?? 0

  return versionsSize + reasoningSize
}

function truncateText(text: string, maxLength: number): string {
  if (text.length <= maxLength) {
    return text
  }

  if (maxLength <= TRUNCATED_CONTENT_SUFFIX.length) {
    return text.slice(0, maxLength)
  }

  return `${text.slice(0, maxLength - TRUNCATED_CONTENT_SUFFIX.length)}${TRUNCATED_CONTENT_SUFFIX}`
}

type SectionOccurrence = {
  heading: string
  index: number
}

function getSectionOccurrences(text: string): SectionOccurrence[] {
  const occurrences: SectionOccurrence[] = []
  const matches = text.matchAll(SECTION_HEADING_LINE_PATTERN)
  for (const match of matches) {
    const index = match.index
    if (index === undefined) {
      continue
    }

    occurrences.push({
      heading: match[0],
      index,
    })
  }

  return occurrences
}

function getHeadingCounts(
  occurrences: SectionOccurrence[]
): Map<string, number> {
  const counts = new Map<string, number>()

  for (const occurrence of occurrences) {
    counts.set(occurrence.heading, (counts.get(occurrence.heading) ?? 0) + 1)
  }

  return counts
}

function findLastRepeatedSectionRunStart(text: string): number {
  const occurrences = getSectionOccurrences(text)
  const headingCounts = getHeadingCounts(occurrences)
  const lastRepeatedIndexes: number[] = []
  const seenHeadings = new Set<string>()

  for (let index = occurrences.length - 1; index >= 0; index--) {
    const occurrence = occurrences[index]
    const count = headingCounts.get(occurrence.heading) ?? 0

    if (
      count < MIN_REPEATED_SECTION_COUNT ||
      seenHeadings.has(occurrence.heading)
    ) {
      continue
    }

    seenHeadings.add(occurrence.heading)
    lastRepeatedIndexes.push(occurrence.index)
  }

  if (lastRepeatedIndexes.length === 0) {
    return -1
  }

  return Math.min(...lastRepeatedIndexes)
}

function collapseRepeatedSectionSnapshots(text: string): string {
  if (text.length < MIN_PREFIX_COLLAPSE_LENGTH) {
    return text
  }

  const lastRepeatedRunStart = findLastRepeatedSectionRunStart(text)
  if (lastRepeatedRunStart === -1) {
    return text
  }

  return text.slice(lastRepeatedRunStart)
}

function normalizeStoredMessageForLoad(message: Message): Message {
  let changed = false
  const versions = message.versions.map((version) => {
    const collapsedContent = collapseRepeatedSectionSnapshots(version.content)
    const content = truncateText(collapsedContent, MAX_LOADED_MESSAGE_CHARS)

    if (content === version.content && collapsedContent === version.content) {
      return version
    }

    changed = true
    return {
      ...version,
      content,
    }
  })

  const reasoning = message.reasoning
    ? {
        ...message.reasoning,
        content: truncateText(
          message.reasoning.content,
          MAX_LOADED_MESSAGE_CHARS
        ),
      }
    : undefined

  if (reasoning?.content !== message.reasoning?.content) {
    changed = true
  }

  const normalized = changed ? { ...message, versions, reasoning } : message

  if (!isAssistantMessagePending(normalized)) {
    return normalized
  }

  const hasContent = hasMessageContent(normalized)
  const hasReasoning = normalized.reasoning?.content.trim()

  if (!hasContent && !hasReasoning) {
    return normalized
  }

  const completedAt =
    normalized.completedAt ??
    normalized.reasoning?.completedAt ??
    normalized.startedAt ??
    normalized.createdAt ??
    Date.now()

  return completeAssistantTiming(
    {
      ...finalizeMessage(normalized),
      status: MESSAGE_STATUS.COMPLETE,
      isReasoningStreaming: false,
    },
    completedAt
  )
}

function trimMessagesByContentSize(messages: Message[]): Message[] {
  let totalSize = 0
  const result: Message[] = []

  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index]
    const messageSize = getMessageSize(message)

    if (
      result.length > 0 &&
      totalSize + messageSize > MAX_LOADED_MESSAGES_CHARS
    ) {
      break
    }

    totalSize += messageSize
    result.push(message)
  }

  return result.reverse()
}

function prepareMessagesForUse(messages: Message[]): Message[] {
  const normalized = messages.map(normalizeStoredMessageForLoad)
  const trimmed = trimMessages(normalized)
  const sizeTrimmed = trimMessagesByContentSize(trimmed)
  return sanitizeMessagesOnLoad(sizeTrimmed)
}

/**
 * Load playground config from localStorage
 */
export function loadConfig(): Partial<PlaygroundConfig> {
  try {
    const result =
      readConfigFromKey(STORAGE_KEYS.CONFIG, false) ??
      readConfigFromKey(STORAGE_KEYS.LEGACY_CONFIG, true)

    if (!result) return {}

    if (result.migrated) {
      saveConfig(result.data)
    }

    return result.data
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load config:', error)
  }
  return {}
}

/**
 * Save playground config to localStorage
 */
export function saveConfig(config: Partial<PlaygroundConfig>): void {
  try {
    const parsed = playgroundConfigSchema.parse(config)
    writeStoredValue(STORAGE_KEYS.CONFIG, parsed)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save config:', error)
  }
}

/**
 * Load parameter enabled state from localStorage
 */
export function loadParameterEnabled(): Partial<ParameterEnabled> {
  try {
    const result =
      readParameterEnabledFromKey(STORAGE_KEYS.PARAMETER_ENABLED, false) ??
      readParameterEnabledFromKey(
        STORAGE_KEYS.LEGACY_PARAMETER_ENABLED,
        true
      ) ??
      readParameterEnabledFromKey(STORAGE_KEYS.LEGACY_CONFIG, true)

    if (!result) return {}

    if (result.migrated) {
      saveParameterEnabled(result.data)
    }

    return result.data
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load parameter enabled:', error)
  }
  return {}
}

/**
 * Save parameter enabled state to localStorage
 */
export function saveParameterEnabled(
  parameterEnabled: Partial<ParameterEnabled>
): void {
  try {
    const parsed = parameterEnabledSchema.parse(parameterEnabled)
    writeStoredValue(STORAGE_KEYS.PARAMETER_ENABLED, parsed)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save parameter enabled:', error)
  }
}

/**
 * Load messages from localStorage
 */
export function loadMessages(): Message[] | null {
  try {
    const result =
      readMessagesFromKey(STORAGE_KEYS.MESSAGES, false, true) ??
      readMessagesFromKey(STORAGE_KEYS.LEGACY_MESSAGES, true, false)
    if (!result) return null

    const prepared = prepareMessagesForUse(result.data)

    if (result.migrated || prepared !== result.data) {
      saveMessages(prepared)
    }

    return prepared
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load messages:', error)
  }
  return null
}

/**
 * Save messages to localStorage
 */
export function saveMessages(messages: Message[]): void {
  try {
    const trimmed = trimMessages(messages)
    const parsed = messagesSchema.parse(trimmed) as Message[]
    writeStoredValue(STORAGE_KEYS.MESSAGES, parsed)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save messages:', error)
  }
}

export function createPlaygroundBackup(
  data: PlaygroundBackupData
): PlaygroundBackupFile {
  return {
    format: 'new-api-playground-backup',
    version: 1,
    exportedAt: new Date().toISOString(),
    config: playgroundConfigSchema.parse(data.config) as PlaygroundConfig,
    parameterEnabled: parameterEnabledSchema.parse(
      data.parameterEnabled
    ) as ParameterEnabled,
    messages: messagesSchema.parse(trimMessages(data.messages)) as Message[],
  }
}

export function parsePlaygroundBackup(value: unknown): PlaygroundBackupData {
  const source = unwrapStoredValue(value)
  if (!isRecord(source)) {
    throw new Error('Invalid playground backup')
  }

  const configSource = isRecord(source.config) ? source.config : source
  const parameterSource = isRecord(source.parameterEnabled)
    ? source.parameterEnabled
    : configSource
  const importedConfig = readConfigFromValue(configSource)
  const importedParameterEnabled =
    readParameterEnabledFromValue(parameterSource)
  const importedMessages =
    'messages' in source ? readMessagesFromValue(source.messages) : []

  if (
    !importedConfig ||
    !importedParameterEnabled ||
    importedMessages === null
  ) {
    throw new Error('Invalid playground backup')
  }

  return {
    config: { ...DEFAULT_CONFIG, ...importedConfig },
    parameterEnabled: {
      ...DEFAULT_PARAMETER_ENABLED,
      ...importedParameterEnabled,
    },
    messages: prepareMessagesForUse(importedMessages),
  }
}

/**
 * Clear all playground data
 */
export function clearPlaygroundData(): void {
  try {
    localStorage.removeItem(STORAGE_KEYS.CONFIG)
    localStorage.removeItem(STORAGE_KEYS.PARAMETER_ENABLED)
    localStorage.removeItem(STORAGE_KEYS.MESSAGES)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to clear playground data:', error)
  }
}
