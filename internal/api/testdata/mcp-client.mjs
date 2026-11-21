import { pathToFileURL } from 'node:url';
import { join } from 'node:path';

const root = join(process.env.LISEUR_MCP_SDK, 'dist', 'esm', 'client');
const { Client } = await import(pathToFileURL(join(root, 'index.js')).href);
const { StreamableHTTPClientTransport } = await import(pathToFileURL(join(root, 'streamableHttp.js')).href);
const client = new Client({ name: 'interop-test', version: '1.0.0' });
const transport = new StreamableHTTPClientTransport(new URL(process.env.MCP_ENDPOINT), {
  requestInit: { headers: { Authorization: `Bearer ${process.env.MCP_ACCESS}` } },
});

try {
  await client.connect(transport);
  const { tools } = await client.listTools();
  const names = new Set(tools.map(tool => tool.name));
  if (!names.has('search_library') || names.has('get_reading_status') || names.has('read_chapter')) {
    throw new Error('Tool discovery did not respect the library-only grant');
  }
  let denied = false;
  try {
    await client.callTool({ name: 'read_chapter', arguments: { book_id: process.env.MCP_BOOK_ID, chapter: 0 } });
  } catch (error) {
    if (error.code !== -32602 || !error.message.includes('unknown tool: read_chapter')) throw error;
    denied = true;
  }
  if (!denied) throw new Error('A text tool accepted a library-only grant');
  const result = await client.callTool({ name: 'search_library', arguments: { query: 'interop' } });
  if (result.isError || !result.structuredContent?.books?.some(book => book.book_id === process.env.MCP_BOOK_ID)) {
    throw new Error('Library search did not return the granted book');
  }
} finally {
  await client.close();
}
