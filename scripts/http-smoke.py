#!/usr/bin/env python3
"""Exercise a disposable deployment. Password is supplied through stdin."""
import base64
import json
import os
import sys
import urllib.error
import urllib.request

base = os.environ.get('SMOKE_APP_URL', 'http://127.0.0.1:18080')
mcp = os.environ.get('SMOKE_MCP_URL', 'http://127.0.0.1:18081/mcp')
password = sys.stdin.readline().rstrip('\n')

def request(url, data=None, token=None, headers=None):
    hdr = {'Content-Type': 'application/json', 'Accept': 'application/json, text/event-stream'}
    hdr.update(headers or {})
    if token:
        hdr['Authorization'] = 'Bearer ' + token
    body = json.dumps(data).encode() if data is not None else None
    with urllib.request.urlopen(urllib.request.Request(url, body, hdr), timeout=10) as response:
        return json.load(response)

session = request(base + '/api/v1/login', {'username': 'admin', 'password': password})['data']['token']
if '--verify' in sys.argv:
    items = request(base + '/api/v1/items?query=container-smoke', token=session)
    assert 'container-smoke' in json.dumps(items), 'inventory did not survive restart/restore'
    with urllib.request.urlopen(urllib.request.Request(base + '/api/v1/documents/1/content',
        headers={'Authorization': 'Bearer '+session})) as response:
        assert response.read() == b'container document bytes'
    print('Persisted inventory, document bytes and administrator login verified')
    sys.exit(0)
item = request(base + '/api/v1/items', {'name': 'container-smoke'}, session)['data']
boundary = 'inventrospect-smoke-boundary'
payload = ('--'+boundary+'\r\nContent-Disposition: form-data; name="type"\r\n\r\nother\r\n'
           '--'+boundary+'\r\nContent-Disposition: form-data; name="file"; filename="smoke.txt"\r\n'
           'Content-Type: text/plain\r\n\r\ncontainer document bytes\r\n--'+boundary+'--\r\n').encode()
with urllib.request.urlopen(urllib.request.Request(base + '/api/v1/items/'+str(item['id'])+'/documents', payload,
    {'Authorization': 'Bearer '+session, 'Content-Type': 'multipart/form-data; boundary='+boundary})) as response:
    document = json.load(response)['data']
token = request(base + '/api/v1/tokens', {
    'name': 'container-smoke-reader', 'user_id': 1,
    'scopes': ['inventory:read', 'stock:read', 'documents:read']
}, session)['data']['secret']
# No credential is written to stdout or a fixture file.
def rpc(method, params):
    return request(mcp, {'jsonrpc': '2.0', 'id': 1, 'method': method, 'params': params}, token,
                   {'MCP-Protocol-Version': '2025-03-26'})
assert 'result' in rpc('initialize', {'protocolVersion': '2025-03-26', 'capabilities': {},
                                    'clientInfo': {'name': 'container-smoke', 'version': '1'}})
tools = rpc('tools/list', {})['result']['tools']
assert any(tool['name'] == 'inventory_search' for tool in tools)
result = rpc('tools/call', {'name': 'inventory_search', 'arguments': {'query': 'container-smoke'}})
assert 'container-smoke' in json.dumps(result)
document_result = rpc('tools/call', {'name': 'document_get', 'arguments': {'id': document['id']}})['result']
resources = [entry['resource'] for entry in document_result['content'] if entry['type'] == 'resource']
assert resources and base64.b64decode(resources[0]['blob']) == b'container document bytes'
try:
    request(base + '/api/v1/items', {'name': 'forbidden'}, token)
    raise AssertionError('read token allowed writes')
except urllib.error.HTTPError as error:
    assert error.code == 403
print('REST login, inventory creation, MCP authentication/discovery/search/document bytes and scope rejection verified')
