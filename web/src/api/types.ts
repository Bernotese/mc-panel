import type { components } from './schema'

type Schemas = components['schemas']

export type ErrorBody = Schemas['Error']
export type ErrorCode = Schemas['Error']['error']
export type Role = Schemas['Role']
export type Stage = Schemas['Stage']
export type Locale = Schemas['Locale']
export type Permission = Schemas['Permission']
export type SecondFactor = Schemas['SecondFactor']
export type Me = Schemas['Me']
export type Passkey = Schemas['Passkey']
export type PasskeyCreated = Schemas['PasskeyCreated']
export type TotpSetupResponse = Schemas['TotpSetupResponse']
export type RecoveryCodesResponse = Schemas['RecoveryCodesResponse']
export type SudoResponse = Schemas['SudoResponse']
export type WebAuthnOptions = Schemas['WebAuthnOptions']
export type Session = Schemas['Session']
export type User = Schemas['User']
export type Server = Schemas['Server']
export type PermissionGrant = Schemas['PermissionGrant']
export type UserPermissions = Schemas['UserPermissions']
export type AuditEntry = Schemas['AuditEntry']
export type AuditPage = Schemas['AuditPage']
export type AuditVerify = Schemas['AuditVerify']
export type AuditResult = Schemas['AuditEntry']['result']
export type SeriesPoint = Schemas['SeriesPoint']
export type NewDeviceLogin = Schemas['NewDeviceLogin']
export type SecurityOverview = Schemas['SecurityOverview']
export type SecurityRange = Schemas['SecurityOverview']['range']
export type LoginAttempt = Schemas['LoginAttempt']
export type AttemptStage = Schemas['LoginAttempt']['stage']
export type LoginAttemptPage = Schemas['LoginAttemptPage']
export type Jail = Schemas['Jail']
export type IPRule = Schemas['IPRule']
export type IPRuleKind = Schemas['IPRule']['kind']
export type SecuritySettings = Schemas['SecuritySettings']
export type SecuritySettingsResponse = Schemas['SecuritySettingsResponse']
export type Notifier = Schemas['Notifier']
export type NotifierKind = Schemas['Notifier']['kind']
export type AgentStatus = Schemas['AgentStatus']
export type PowerAction = Schemas['PowerRequest']['action']
export type PowerResponse = Schemas['PowerResponse']
export type InstanceType = Schemas['InstanceType']
export type JavaVersion = Schemas['JavaVersion']
export type ServerState = Schemas['ServerState']
export type ServerProperties = Schemas['ServerProperties']
export type Difficulty = NonNullable<ServerProperties['difficulty']>
export type Gamemode = NonNullable<ServerProperties['gamemode']>
export type LevelType = NonNullable<ServerProperties['level_type']>
export type InstanceConfig = Schemas['InstanceConfig']
export type ServerStatus = Schemas['ServerStatus']
export type ServerOperation = Schemas['ServerStatus']['operation']
export type Catalog = Schemas['Catalog']
export type CatalogVersion = Schemas['CatalogVersion']
export type ServerSettings = Schemas['ServerSettings']
export type DockerState = Schemas['ServerSettings']['docker']
export type UnknownContainer = Schemas['UnknownContainer']
export type CreateServerRequest = Schemas['CreateServerRequest']
export type UpdateServerRequest = Schemas['UpdateServerRequest']
export type UpdateServerSettingsRequest = Schemas['UpdateServerSettingsRequest']
export type ConsoleCommandRequest = Schemas['ConsoleCommandRequest']
export type ConsoleCommandResponse = Schemas['ConsoleCommandResponse']
export type OnlinePlayer = Schemas['OnlinePlayer']
export type OnlinePlayers = Schemas['OnlinePlayers']
export type PlayerOp = Schemas['PlayerOp']
export type PlayerEntry = Schemas['PlayerEntry']
export type PlayerBan = Schemas['PlayerBan']
export type PlayerLists = Schemas['PlayerLists']
export type PlayerListFile = PlayerLists['errors'][number]
export type PlayerAction = Schemas['PlayerAction']
export type PlayerActionRequest = Schemas['PlayerActionRequest']
export type FileEntryType = Schemas['FileEntryType']
export type FileEntry = Schemas['FileEntry']
export type FileList = Schemas['FileList']
export type FileContent = Schemas['FileContent']
export type WriteFileRequest = Schemas['WriteFileRequest']
export type FileWriteResult = Schemas['FileWriteResult']
export type UploadResult = Schemas['UploadResult']
export type FilePathRequest = Schemas['FilePathRequest']
export type RenameFileRequest = Schemas['RenameFileRequest']
export type ExtractRequest = Schemas['ExtractRequest']
export type FileJobState = Schemas['FileJobState']
export type FileJob = Schemas['FileJob']
export type FileJobResponse = Schemas['FileJobResponse']

/** SSE `line` event of GET /servers/{id}/console/stream; the event id is the Docker timestamp in ns. */
export interface ConsoleLineEvent {
  ts: number // unix ms
  stream: 'out' | 'err'
  line: string
}

/** SSE `state` event: the container log stream (re)opened (true) or ended (false). */
export interface ConsoleStateEvent {
  running: boolean
}

export const ALL_PERMISSIONS: readonly Permission[] = [
  'server.view',
  'server.power',
  'server.console',
  'server.files',
  'server.plugins',
  'server.backups',
]
