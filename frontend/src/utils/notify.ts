import { ElMessage, type MessageHandler, type MessageType } from 'element-plus'

const OPERATION_MESSAGE_OFFSET = 82
const OPERATION_MESSAGE_DURATION = 5000

function notify(message: string, type: MessageType): MessageHandler {
  return ElMessage({
    message,
    type,
    duration: OPERATION_MESSAGE_DURATION,
    offset: OPERATION_MESSAGE_OFFSET,
    grouping: true,
    showClose: false,
    customClass: 'maxkb-operation-message',
  })
}

export function notifySuccess(message: string): MessageHandler {
  return notify(message, 'success')
}

export function notifyWarning(message: string): MessageHandler {
  return notify(message, 'warning')
}

export function notifyError(message: string): MessageHandler {
  return notify(message, 'error')
}

export function notifyInfo(message: string): MessageHandler {
  return notify(message, 'info')
}
