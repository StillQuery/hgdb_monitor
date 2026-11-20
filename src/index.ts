import { chmod, mkdir } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Context } from '@deepseek-ai/cordis'
import { dshHomePath } from '@deepseek-ai/dsh-home-paths'
import type {} from '@deepseek-ai/dsh-subprocess'
import type {} from '@deepseek-ai/dsh-tools'
import type {} from '@deepseek-ai/dsh-host-webserver'
import { CoreClient } from './core-client.js'
import { registerManagementApi } from './http.js'
import { PluginLogger } from './logger.js'
import { registerMonitorTools } from './tools.js'

export const name = 'hgdb_monitor'
export const inject = ['subprocess', 'tools', 'webServer']

function coreExecutable(): string {
  if (process.arch !== 'x64') throw new Error(`hgdb_monitor does not support architecture ${process.arch}`)
  const file = process.platform === 'win32'
    ? 'hgdb-monitor-core-windows-amd64.exe'
    : process.platform === 'linux'
      ? 'hgdb-monitor-core-linux-amd64'
      : undefined
  if (file === undefined) throw new Error(`hgdb_monitor does not support platform ${process.platform}`)
  return fileURLToPath(new URL(`../core/bin/${file}`, import.meta.url))
}

export function apply(ctx: Context): void {
  ctx.effect(() => {
    const dataDir = dshHomePath('data', 'hgdb_monitor')
    const logger = new PluginLogger(dshHomePath('logs', 'hgdb_monitor'))
    const executable = coreExecutable()
    const client = new CoreClient(ctx, executable, dirname(executable), dataDir)
    let disposed = false
    let disposeTools: (() => void) | undefined
    let disposeApi: (() => void) | undefined = registerManagementApi(ctx, client, logger)
    logger.info('plugin_start')

    const startup = Promise.all([
      mkdir(dataDir, { recursive: true }),
      ...(process.platform === 'linux' ? [chmod(executable, 0o755)] : []),
    ])
      .then(() => client.start())
      .then(() => client.call('storage.status'))
      .then(() => {
        if (!disposed) {
          disposeTools = registerMonitorTools(ctx, client)
          logger.info('core_ready')
          ctx.logger.info(`hgdb_monitor: core ready; data directory ${join('$DSH_HOME', 'data', 'hgdb_monitor')}`)
        }
      })
      .catch(error => {
        if (!disposed) {
          logger.error('core_startup_failed', {
            message: error instanceof Error ? error.message : String(error),
            ...(error instanceof Error && error.stack !== undefined ? { stack: error.stack } : {}),
          })
          ctx.logger.error(`hgdb_monitor: core startup failed: ${String(error)}`)
        }
      })

    return () => {
      disposed = true
      disposeTools?.()
      disposeTools = undefined
      disposeApi?.()
      disposeApi = undefined
      logger.info('plugin_stop')
      return startup.then(
        () => client.dispose(),
        () => client.dispose(),
      ).finally(() => logger.flush())
    }
  }, 'hgdb_monitor: core lifecycle')
}
