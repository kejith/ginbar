export const INBOX_PAGE_SIZE = 50;
export const THREAD_PAGE_SIZE = 50;
export const MAX_RETAINED_CONVERSATIONS = 200;
export const MAX_RETAINED_MESSAGES = 300;
export const MAX_RETAINED_THREADS = 4;
export const MAX_PENDING_SENDS = 8;
export const MAX_MESSAGE_CHARACTERS = 10000;

/**
 * @typedef {{id:number, username?:string, available:boolean}} InboxPeer
 * @typedef {{id:number, senderId:number, createdAt:string}} LatestMessage
 * @typedef {{peer:InboxPeer, latestMessage:LatestMessage}} ConversationSummary
 * @typedef {{id:number, senderId:number, recipientId:number, body:string, createdAt:string}} PrivateMessage
 * @typedef {{kind:"inbox"}|{kind:"thread", peerId:number}} MessagesRoute
 */

/** @param {string} pathname @returns {MessagesRoute|null} */
export function messageRouteFromPath(pathname) {
  if (pathname === "/messages" || pathname === "/messages/") return { kind: "inbox" };
  const match = /^\/messages\/([1-9]\d*)\/?$/.exec(pathname);
  if (!match) return null;
  const peerId = Number(match[1]);
  if (!Number.isSafeInteger(peerId) || peerId <= 0) return null;
  return { kind: "thread", peerId };
}

/** @param {number} peerId */
export function pathForMessagePeer(peerId) {
  if (!Number.isSafeInteger(peerId) || peerId <= 0) throw new Error("peer id must be a positive safe integer");
  return `/messages/${peerId}`;
}

/**
 * @param {ConversationSummary[]} current
 * @param {ConversationSummary[]} incoming
 * @param {boolean} replace
 * @param {number} maxRetained
 * @returns {ConversationSummary[]}
 */
export function mergeInboxConversations(
  current,
  incoming,
  replace = false,
  maxRetained = MAX_RETAINED_CONVERSATIONS,
) {
  const byPeer = new Map();
  const source = replace ? incoming : [...current, ...incoming];
  for (const conversation of source) {
    const existing = byPeer.get(conversation.peer.id);
    if (!existing || conversation.latestMessage.id > existing.latestMessage.id) {
      byPeer.set(conversation.peer.id, conversation);
    }
  }
  return [...byPeer.values()]
    .sort((a, b) => b.latestMessage.id - a.latestMessage.id || b.peer.id - a.peer.id)
    .slice(0, Math.max(0, maxRetained));
}

/**
 * @param {PrivateMessage[]} current
 * @param {PrivateMessage[]} incoming
 * @param {boolean} replace
 * @param {number} maxRetained
 * @returns {PrivateMessage[]}
 */
export function mergeThreadMessages(
  current,
  incoming,
  replace = false,
  maxRetained = MAX_RETAINED_MESSAGES,
) {
  const byID = new Map();
  const source = replace ? incoming : [...current, ...incoming];
  for (const message of source) {
    if (!byID.has(message.id)) byID.set(message.id, message);
  }
  return [...byID.values()]
    .sort((a, b) => b.id - a.id)
    .slice(0, Math.max(0, maxRetained));
}

/** @param {PrivateMessage[]} messages @returns {PrivateMessage[]} */
export function messagesForDisplay(messages) {
  return [...messages].reverse();
}

/**
 * @param {number[]} current
 * @param {number} peerId
 * @param {number} maxRetained
 * @returns {number[]}
 */
export function touchThreadOrder(current, peerId, maxRetained = MAX_RETAINED_THREADS) {
  return [peerId, ...current.filter((id) => id !== peerId)].slice(0, Math.max(0, maxRetained));
}

/**
 * @param {number|null} requestPeerId
 * @param {number} requestEpoch
 * @param {number|null} currentPeerId
 * @param {number} currentEpoch
 */
export function requestStillCurrent(requestPeerId, requestEpoch, currentPeerId, currentEpoch) {
  return requestEpoch === currentEpoch && requestPeerId === currentPeerId;
}

/** @param {string} value */
export function messageBodyLength(value) {
  return Array.from(value).length;
}

/** @param {string} value */
export function clampMessageBody(value) {
  const characters = Array.from(value);
  return characters.length <= MAX_MESSAGE_CHARACTERS
    ? value
    : characters.slice(0, MAX_MESSAGE_CHARACTERS).join("");
}
