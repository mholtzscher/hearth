#!/usr/bin/env python3
"""One opt-in A5 journey against an already running, isolated simulator stack."""
import argparse
import json
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PROMPT = 'create automation for the office button that turns the fan and 2 lamps on for single press. and turns them off for double press'
REPLY = 'Use Office Plug Fan.'
INITIAL = {
    ('office-button', 'events'): None,
    ('office-plug-fan', 'power'): False,
    ('office-ceiling-fan', 'power'): False,
    ('office-desk-lamp', 'power'): False,
    ('office-desk-lamp', 'brightness'): 37,
    ('office-desk-lamp', 'colortemp'): {'active': True, 'value': 370},
    ('office-corner-lamp', 'power'): False,
    ('office-corner-lamp', 'brightness'): 63,
    ('office-corner-lamp', 'colortemp'): {'active': True, 'value': 290},
    ('kitchen-light', 'power'): True,
    ('hall-light', 'power'): True,
}
TARGETS = [('office-plug-fan', 'power'), ('office-desk-lamp', 'power'), ('office-corner-lamp', 'power')]


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def loopback(value):
    url = urllib.parse.urlsplit(value)
    if url.scheme != 'http' or url.hostname != '127.0.0.1' or not url.port or url.path not in ('', '/') or url.query or url.fragment or url.username:
        raise argparse.ArgumentTypeError('use explicit http://127.0.0.1:PORT only')
    return value.rstrip('/')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--core', required=True, type=loopback)
    parser.add_argument('--sim', required=True, type=loopback)
    parser.add_argument('--db', required=True, type=Path)
    parser.add_argument('--evidence', required=True, type=Path, help='new directory, outside runtime storage')
    parser.add_argument('--model-settings', required=True, help='nonsecret actual model and settings used by running Core')
    args = parser.parse_args()
    db = args.db.resolve()
    check(db == (ROOT / '.data/simulator-stack/storage/hearthd.db').resolve() and db.is_file(), 'DB must be this worktree stack\'s existing hearthd.db')
    evidence = args.evidence.resolve()
    check(not evidence.is_relative_to((ROOT / '.data/simulator-stack/storage').resolve()), 'evidence must be outside runtime storage')
    evidence.mkdir(parents=True, exist_ok=False)
    counter = 0
    conversation = None
    result = {'status': 'failed', 'human_clarification_review': 'not reached'}
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def save(name, value):
        (evidence / (name + '.json')).write_text(json.dumps(value, indent=2, ensure_ascii=False) + '\n')
        return value

    def http(base, path, method='GET', body=None, timeout=2):
        nonlocal counter
        counter += 1
        record = {'method': method, 'url': base + path, 'request': body, 'status': None, 'response': None}
        request = urllib.request.Request(base + path, method=method, data=None if body is None else json.dumps(body).encode(), headers={'Content-Type': 'application/json'})
        save(f'http-{counter:04d}', record)
        try:
            try:
                response = opener.open(request, timeout=timeout)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                record['status'] = response.status
                raw = response.read().decode('utf-8', errors='replace')
                record['response'] = raw
            save(f'http-{counter:04d}', record)
            check(200 <= record['status'] < 300, f'{method} {path}: HTTP {record["status"]}')
            return json.loads(raw) if raw else None
        except (Exception, KeyboardInterrupt) as error:
            record['error'] = str(error)
            record['mutation_may_have_taken_effect'] = method != 'GET'
            save(f'http-{counter:04d}', record)
            raise

    def get(path):
        return http(args.core, path)

    def page(path):
        data = get(path + ('&' if '?' in path else '?') + 'limit=200')
        check(not data.get('next_cursor'), f'pagination makes evidence incomplete: {path}')
        return data['items']

    def poll(name, read, predicate):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            value = read()
            if predicate(value):
                return save(name, value)
            time.sleep(min(0.5, max(0, deadline - time.monotonic())))
        raise AssertionError(name + ': evidence deadline exceeded')

    def raw_history():
        uri = db.as_uri() + '?mode=ro'
        with sqlite3.connect(uri, uri=True, timeout=2) as conn:
            rows = conn.execute('SELECT id, role, message_json, created_at FROM agent_messages WHERE conversation_id = ? ORDER BY id', (conversation,)).fetchall()
        return [{'id': row[0], 'role': row[1], 'message_json': row[2], 'message': json.loads(row[2]), 'created_at': row[3]} for row in rows]

    def tools(rows):
        calls, results = [], {}
        for row in rows:
            msg = row['message']
            calls.extend(msg.get('tool_calls', []))
            if msg.get('role') == 'tool':
                results[msg.get('tool_call_id')] = msg.get('content', '')
        return calls, results

    def commands(entity):
        return page(f'/v1/entities/{entity}/commands')

    def all_commands():
        return {key: commands(entity) for key, entity in ids.items() if key != ('office-button', 'events')}

    def command_count():
        with sqlite3.connect(db.as_uri() + '?mode=ro', uri=True, timeout=2) as conn:
            return conn.execute('SELECT count(*) FROM commands').fetchone()[0]

    try:
        for name, command in [('revision', ['git', 'rev-parse', 'HEAD']), ('status', ['git', 'status', '--short']), ('diff', ['git', 'diff', 'HEAD', '--binary'])]:
            (evidence / (name + '.txt')).write_text(subprocess.run(command, cwd=ROOT, check=True, capture_output=True, text=True, timeout=10).stdout)
        save('settings', {'operator_supplied_nonsecret_model_settings': args.model_settings, 'db': str(db), 'core': args.core, 'sim': args.sim})
        get('/readyz')
        inventory = http(args.sim, '/v1/sim/entities')
        check(len(inventory) == len(INITIAL), 'unexpected simulator inventory; use fresh dedicated preset')
        ids = {}
        for item in inventory:
            key = (item['binding_key'], item['key'])
            check(item['adapter_id'] == 'sim-agent-eval' and key in INITIAL and key not in ids, 'unexpected or duplicate simulator Entity')
            ids[key] = item['entity_id']
        check(set(ids) == set(INITIAL), 'missing fixture Entities')
        check({x['id'] for x in page('/v1/entities')} == set(ids.values()), 'Core inventory differs from fixture')
        check(not page('/v1/automations'), 'baseline contains Automations')
        check(not get('/v1/agent/conversations')['conversations'], 'baseline contains conversations')
        check(command_count() == 0, 'baseline contains Commands')
        for key, entity in ids.items():
            value = poll('baseline-' + '-'.join(key), lambda: get('/v1/entities/' + entity),
                         lambda x: (x.get('state') is None if INITIAL[key] is None else x.get('state') is not None and x['state']['value'] == INITIAL[key]))
            check(value['id'] == entity, 'Core Entity identity mismatch')
        check(not page(f'/v1/entities/{ids[("office-button", "events")]}/events'), 'baseline contains button Events')
        check(all(not x for x in all_commands().values()), 'baseline contains Entity Commands')
        conversation = http(args.core, '/v1/agent/conversations', 'POST')['id']
        first = http(args.core, f'/v1/agent/conversations/{conversation}/messages', 'POST', {'text': PROMPT}, timeout=180)
        save('history-first', get(f'/v1/agent/conversations/{conversation}/messages'))
        rows1 = save('raw-messages-first', raw_history())
        calls1, results1 = tools(rows1)
        text1 = '\n'.join(results1.values()).lower()
        check('office plug fan' in text1 and 'office ceiling fan' in text1, 'first turn lacks tool-result evidence for both fans')
        check(not any(c.get('function', {}).get('name') == 'create_automation' for c in calls1), 'first turn attempted Automation creation')
        check(not page('/v1/automations'), 'first turn saved an Automation before clarification')
        check(command_count() == 0 and all(not x for x in all_commands().values()), 'first turn dispatched Commands')
        print('\nFirst reply:\n' + first['reply'], flush=True)
        print('Does this ask which of the two Office fans to use, without claiming a save? [y/N] ', end='', flush=True)
        accepted = input().strip().lower() == 'y'
        result['human_clarification_review'] = 'accepted' if accepted else 'rejected'
        save('result', result)
        check(accepted, 'human rejected or did not confirm clarification')
        second = http(args.core, f'/v1/agent/conversations/{conversation}/messages', 'POST', {'text': REPLY}, timeout=180)
        save('history-final', get(f'/v1/agent/conversations/{conversation}/messages'))
        rows = save('raw-messages-final', raw_history())
        calls, results = tools(rows)
        check(all(call.get('id') and call['id'] in results for call in calls), 'missing tool call/result pairing')
        skill = [c for c in calls if c.get('function', {}).get('name') == 'skill' and 'hearth-automations' in c['function'].get('arguments', '')]
        check(skill and all('# Hearth Automations' in results[c['id']] for c in skill), 'native skill load/result not established')
        readbacks = [c for c in calls[len(calls1):] if c.get('function', {}).get('name') == 'get_automation']
        check(readbacks, 'no saved-definition readback tool call')
        automations = page('/v1/automations')
        check(len(automations) == 1, 'expected exactly one Automation')
        automation = automations[0]['id']
        definition = save('automation', get('/v1/automations/' + automation))
        check(definition['definition']['enabled'] is True, 'Automation not enabled')
        def matches_readback(call):
            try:
                arguments = json.loads(call['function']['arguments'])
                response = json.loads(results[call['id']])
                return (arguments.get('automation_id') == automation and response.get('automation_id') == automation
                        and response.get('revision') == definition['revision'] and response.get('definition') == definition['definition'])
            except (ValueError, KeyError, TypeError, AttributeError):
                return False
        check(any(matches_readback(c) for c in readbacks), 'readback does not match saved Automation and revision')
        check(command_count() == 0 and all(not x for x in all_commands().values()), 'authoring dispatched Commands')
        event_entity = ids[('office-button', 'events')]
        expected_commands = set()
        for event_name, power in [('single', True), ('double', False)]:
            publication = http(args.sim, f'/v1/sim/entities/{event_entity}/publish', 'POST', {'name': event_name})
            event = publication['event_id']
            poll('event-' + event_name, lambda: page(f'/v1/entities/{event_entity}/events'), lambda items: any(x['event_id'] == event and x['name'] == event_name and x['disposition'] == 'accepted' for x in items))
            history = poll('history-' + event_name, lambda: page(f'/v1/automations/{automation}/history'), lambda items: any(x.get('cause', {}).get('fact', {}).get('event_id') == event and x['kind'] == 'run' and x['status'] == 'succeeded' for x in items))
            matching = [x for x in history if x.get('cause', {}).get('fact', {}).get('event_id') == event]
            check(len(matching) == 1 and matching[0]['kind'] == 'run', 'event did not produce exactly one Run')
            run = save('run-' + event_name, get(f'/v1/automations/{automation}/history/{matching[0]["id"]}')['run'])
            check(run['status'] == 'succeeded', 'Run not succeeded')
            # Legacy steps and newer nested execution records both retain verified IDs.
            def collect(value):
                if isinstance(value, dict):
                    return ([value['verified_command_id']] if value.get('verified_command_id') else []) + sum((collect(x) for x in value.values()), [])
                if isinstance(value, list):
                    return sum((collect(x) for x in value), [])
                return []
            command_ids = collect(run)
            check(len(command_ids) == 3 and len(set(command_ids)) == 3, 'Run must verify exactly three Commands')
            observed = []
            for command_id in command_ids:
                command = get('/v1/commands/' + command_id)
                check(command['status'] == 'satisfied' and command['operation'] == 'set' and command['parameters'] == {'value': power}, 'wrong Command outcome or parameters')
                observed.append(command['entity_id'])
                expected_commands.add(command_id)
            check(set(observed) == {ids[x] for x in TARGETS}, 'wrong Command targets')
            for key, entity in ids.items():
                state = get('/v1/entities/' + entity)['state']
                expected = power if key in TARGETS else INITIAL[key]
                check(state is None if expected is None else state is not None and state['value'] == expected, 'wrong or changed State: ' + str(key))
        histories = all_commands()
        actual = {command['id'] for items in histories.values() for command in items}
        check(actual == expected_commands and len(actual) == 6, 'unexpected or missing Command history')
        check(command_count() == 6, 'unexpected global Command count')
        check(all(not histories[key] for key in histories if key not in TARGETS), 'unrelated Entity received a Command')
        check(len(page(f'/v1/automations/{automation}/history')) == 2, 'unexpected Automation history')
        result.update(status='passed', conversation_id=conversation, automation_id=automation, commands=sorted(actual), note='Human clarification review required; synthetic Devices do not prove physical behavior.')
        save('result', result)
        print('PASS; evidence:', evidence)
    except (Exception, KeyboardInterrupt) as error:
        result['error'] = str(error)
        if conversation:
            try:
                save('raw-messages-on-failure', raw_history())
            except Exception as history_error:
                result['history_capture_error'] = str(history_error)
        save('result', result)
        print('FAIL; inspect retained evidence, do not retry mutations:', evidence, file=sys.stderr)
        print(str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
