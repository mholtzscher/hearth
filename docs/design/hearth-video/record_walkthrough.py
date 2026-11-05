import json
import pathlib
import subprocess
import time

ROOT = pathlib.Path(__file__).resolve().parent
SESSION = 'hearth-feature-video'
RAW = ROOT / 'hearth-agent-walkthrough-raw.mp4'
chapters = []
started = None


def browser(*args):
    result = subprocess.run(
        ['agent-browser', '--session', SESSION, '--json', *map(str, args)],
        check=True, text=True, capture_output=True, timeout=45,
    )
    data = json.loads(result.stdout)
    if not data.get('success'):
        raise RuntimeError(data)
    return data.get('data', {})


def js(source):
    return browser('eval', source).get('result')


def pause(seconds=1.7):
    # Deliberate reading time in the recorded presentation, not job polling.
    time.sleep(seconds)


def frame_js(source):
    return js("(() => { const d=document.querySelector('#prototype').contentDocument; " + source + ' })()')


def point(selector, fraction=.5):
    frame_js(f'const e=d.querySelector({json.dumps(selector)}); if(!e)throw new Error("Missing element: "+{json.dumps(selector)}); e.scrollIntoView({{block:"nearest",behavior:"smooth"}});')
    pause(.45)
    return js("(() => { const f=document.querySelector('#prototype'),e=f.contentDocument.querySelector(" + json.dumps(selector) + ");const r=e.getBoundingClientRect(),b=f.getBoundingClientRect();if(!r.width||!r.height)throw new Error('Element is hidden');return {x:b.x+f.clientLeft+r.x+r.width*" + str(fraction) + ",y:b.y+f.clientTop+r.y+r.height/2}; })()")


def click(selector, hold=.6):
    p = point(selector)
    browser('mouse', 'move', round(p['x']), round(p['y']), '--human')
    browser('mouse', 'down', 'left')
    browser('mouse', 'up', 'left')
    pause(hold)


def fill(selector, text):
    click(selector, .15)
    browser('press', 'Control+a')
    browser('keyboard', 'inserttext', text)
    pause(.7)


def wait_text(text):
    browser('wait', '--fn', 'document.querySelector("#prototype").contentDocument.body.innerText.includes(' + json.dumps(text) + ')')


def ask(text):
    fill('#prompt', text)
    click('#send', .2)
    browser('wait', '--fn', '!document.querySelector("#prototype").contentDocument.querySelector("#send").disabled')
    pause(2.4)


def canvas_scroll(top):
    frame_js(f'd.querySelector("#canvas").scrollTo({{top:{top},behavior:"smooth"}});')
    pause(1)


def chapter(number, label, title, description, mobile=False):
    stamp = time.monotonic() - started
    chapters.append({'start': round(stamp, 3), 'title': label, 'description': description})
    print(f'{stamp:6.1f}s  {number:02d}  {label}', flush=True)
    js('window.chapter(' + ','.join(json.dumps(v) for v in [number, label, title, description, mobile]) + ')')
    pause(1.4)


def drag_brightness(target):
    info = frame_js('const e=d.querySelector("#brightness");return {value:Number(e.value)};')
    start = point('#brightness', (info['value']-1)/99)
    end = point('#brightness', (target-1)/99)
    browser('mouse', 'move', round(start['x']), round(start['y']), '--human')
    browser('mouse', 'down', 'left')
    browser('mouse', 'move', round(end['x']), round(end['y']), '--duration', '900', '--steps', '40')
    browser('mouse', 'up', 'left')
    pause(2)


def main():
    global started
    browser('frame', 'main')
    browser('open', 'http://127.0.0.1:19439/stage.html')
    browser('set', 'viewport', '1920', '1080')
    browser('wait', '--fn', '!!document.querySelector("#prototype").contentDocument.querySelector("#prompt")')
    frame_js('d.defaultView.localStorage.removeItem("hearth.design.prototype.dashboards.v1");d.defaultView.location.reload();')
    browser('wait', '--fn', '!!document.querySelector("#prototype").contentDocument.querySelector("#prompt")')
    browser('record', 'start', RAW, '--cursor', '--fps', '30')
    started = time.monotonic()
    try:
        chapter(1, 'A home, in conversation', 'Ask for what you need. Let the interface take shape.', 'Start with a request, not a configuration screen.')
        pause(2)
        click('[data-action="new"]', 2)
        ask('Show me the temperatures throughout the house.')
        click('.trace summary', 2)
        click('.trace summary')
        click('[data-kind="temperature"]', 2)

        chapter(2, 'Explore your home', 'From room readings to the bigger picture.', 'Switch history ranges, inspect exact values, and open sensor details.')
        click('[data-period="24h"]', 2)
        click('[data-action="table"]')
        canvas_scroll(480)
        pause(2.5)
        click('[data-action="table"]')
        canvas_scroll(0)
        click('[data-period="6h"]', 1)
        click('[data-room="office"]', 2.5)
        click('#dialog [data-action="close-dialog"]')

        chapter(3, 'Make it yours', 'Choose the rooms, units, and name that matter.', 'Customize the generated interface. The conversation preview stays in sync.')
        click('[data-action="customize"]', 1)
        fill('#view-title', 'Upstairs comfort')
        click('#unit', .3)
        browser('press', 'ArrowDown')
        browser('press', 'Enter')
        click('input[value="living"]')
        click('input[value="kitchen"]')
        pause(1.5)
        click('#customize-form button[type="submit"]', 2)
        click('[data-action="save"]', 1.5)
        click('#save-form button[type="submit"]', 2)

        chapter(4, 'Keep useful views', 'A conversation becomes a dashboard.', 'Saved views keep their room selection and units across a browser refresh.')
        click('#nav-saved', 2)
        frame_js('d.defaultView.location.reload();')
        browser('wait', '--fn', '!!document.querySelector("#prototype").contentDocument.querySelector("#nav-saved")')
        click('#nav-saved', 2)
        click('[data-action="load-saved"]', 2)
        click('[data-action="save"]')
        fill('#save-title', 'Upstairs comfort · evening')
        click('#save-form button[type="submit"]', 2)

        chapter(5, 'Ask a follow-up', 'Understand the readings. Then decide what to do.', 'Hearth distinguishes observed changes from an explanation it cannot yet prove.')
        click('#messages [data-prompt="Why is the office warmer?"]', .2)
        wait_text('Temperature readings alone')
        pause(4)
        click('#messages .message:last-child .trace summary', 2)
        canvas_scroll(500)
        click('#canvas [data-prompt="Let me know if the office gets too warm"]', .2)
        wait_text('Ready when you are.')
        canvas_scroll(0)
        pause(2)

        chapter(6, 'Automations you can review', 'An editable proposal. An explicit decision.', 'Set a threshold, review the exact action, and inspect a simulated run.')
        fill('#threshold', '24.5')
        browser('press', 'Tab')
        pause(1)
        click('[data-action="approve-automation"]', 3)
        click('#dialog [data-action="close-dialog"]', 1)
        click('[data-action="approve-automation"]', 1)
        click('[data-action="confirm-automation"]', 2)
        click('[data-action="simulate-run"]', 3)
        click('#nav-automations', 2)
        click('#canvas [data-kind="automation"]', 2)
        click('[data-action="disable-automation"]', 2)

        chapter(7, 'Control the room', 'Settle in for a softer evening.', 'Adjust brightness and power. Each simulated action shows an observed outcome.')
        click('#recent-lights', 2)
        canvas_scroll(0)
        drag_brightness(32)
        click('[data-action="power"]', 2)
        click('[data-action="power"]', 1.5)
        drag_brightness(58)

        chapter(8, 'Follow the evidence', 'Why did that light turn on?', 'Trace a motion observation through an automation run to the lamp’s reported state.')
        click('#canvas [data-prompt="Why did the lamp turn on?"]', .2)
        wait_text('There’s a story behind the light.')
        canvas_scroll(0)
        pause(5)
        click('#messages .message:last-child .trace summary', 2)
        click('#canvas [data-kind="lights"]', 1.5)

        chapter(9, 'Clear recovery states', 'A missing reading is not a made-up reading.', 'Developer tools expose the proposed stack and let you explore failure and retry.')
        click('[data-action="developer"]', 2.5)
        canvas_scroll(0)
        pause(2)
        click('#offline', 1)
        click('#fail-next', 1)
        ask('Show me the temperatures throughout the house.')
        wait_text('The demo connection was interrupted')
        pause(2)
        click('[data-action="retry"]', .2)
        wait_text('Bedroom sensor is offline.')
        canvas_scroll(0)
        pause(3)
        click('[data-room="bedroom"]', 3)
        click('#dialog [data-action="close-dialog"]')
        click('[data-action="developer"]')
        click('#offline', .5)
        click('#recent-temperature', 1)

        chapter(10, 'Home, wherever you are', 'The same experience, comfortably on mobile.', 'Switch between the conversation and canvas. Your saved views come with you.', True)
        click('[data-action="new"]', 2)
        ask('Show me the temperatures')
        frame_js('d.querySelector("#messages").scrollTo({top:0,behavior:"smooth"});')
        pause(2)
        click('#tab-canvas', 2)
        canvas_scroll(310)
        pause(2)
        canvas_scroll(0)
        click('[data-action="customize"]', 2.5)
        click('#dialog [data-action="close-dialog"]')
        click('#nav-saved', 1.5)
        click('[data-action="load-saved"]', 2)
        click('#tab-conversation', 1)
        click('#nav-automations', 1.5)

        chapter(11, 'An interface that follows your intent', 'Ask. Shape. Save. Understand.', 'A working experience study for React, Vite, shadcn/ui, assistant-ui, and json-render.')
        click('#nav-saved', 1)
        click('[data-action="delete-saved"]', 2)
        click('[data-action="confirm-delete"]', 2)
        click('[data-action="back-temperature"]')
        click('.topbar [data-action="about"]', 3)
        click('#dialog [data-action="close-dialog"]')
        click('#recent-temperature', 1)
        canvas_scroll(0)
        pause(4)
    finally:
        result = browser('record', 'stop')
        elapsed = time.monotonic() - started
        (ROOT / 'chapters.json').write_text(json.dumps({'duration': elapsed, 'chapters': chapters, 'recording': result}, indent=2))
        print(f'Recording stopped: {elapsed:.1f}s', flush=True)


if __name__ == '__main__':
    main()
