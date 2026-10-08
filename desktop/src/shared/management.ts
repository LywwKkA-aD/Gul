import type { ConnectInput } from './contracts.ts';

export interface ServerInfo {
  readonly protocolVersion: 2;
  readonly serverId: string | null;
  readonly channelManagement: boolean;
  readonly memberAuthentication: boolean;
  readonly maxChannels: 64;
}
export interface MemberIdentity {
  readonly id: string | null;
  readonly role: 'owner' | 'member' | 'guest';
}
export interface ManagementContext {
  readonly epoch: number;
  readonly serverId: string;
}
export interface ChannelPolicy {
  readonly access: 'open' | 'restricted';
  readonly allowedMemberIds: readonly string[];
}
export interface ChannelCreate extends ManagementContext, ChannelPolicy {
  readonly name: string;
  readonly catalogVersion: number;
}
export interface ChannelUpdate extends ManagementContext, ChannelPolicy {
  readonly channelId: number;
  readonly version: number;
  readonly name: string;
}
export interface ChannelDelete extends ManagementContext {
  readonly channelId: number;
  readonly version: number;
}
export interface ChannelPermissions extends ChannelPolicy {
  readonly channelId: number;
  readonly version: number;
}
export interface RegisteredMember {
  readonly id: string;
  readonly name: string;
  readonly role: 'owner' | 'member';
  readonly revoked: boolean;
}
export interface MemberList {
  readonly members: readonly RegisteredMember[];
  readonly catalogVersion: number;
}
export interface Invitation {
  readonly inviteToken: string;
  readonly expiresAtUnixSeconds: number;
}
export interface MemberCredentialInfo {
  readonly state: 'none' | 'loaded' | 'saved' | 'unavailable' | 'unreadable';
  readonly serverId: string | null;
  readonly memberId: string | null;
  readonly rememberIdentity: boolean;
  readonly usable: boolean;
  readonly saveError?: 'unavailable' | 'write-failed';
}
export interface ImportMemberCredential {
  readonly address: string;
  readonly rememberIdentity: boolean;
}
export interface RedeemInvitation {
  readonly input: ConnectInput;
  readonly inviteToken: string;
  readonly rememberIdentity: boolean;
}
