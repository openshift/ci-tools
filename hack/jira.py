#!/usr/bin/env python3
# https://docs.atlassian.com/software/jira/docs/api/REST/latest
import json
import sys
import urllib.parse
import urllib.request


JIRA = 'https://jira.coreos.com'
SEARCH = f'{JIRA}/rest/api/2/search'
BROWSE = f'{JIRA}/browse'
DONE = '10001'


def main():
    if len(sys.argv) != 3:
        print(f'Usage: {sys.argv[0]} session_file sprint', file=sys.stderr)
        sys.exit(1)
    _, session_file, sprint = sys.argv
    ret = search(open(session_file).read().rstrip(), f'sprint="{sprint}"')
    print_issues(*partition(
        json.load(ret)['issues'],
        lambda x: x['fields']['status']['id'] == DONE))


def search(session, jql):
    return urllib.request.urlopen(urllib.request.Request(
        method='GET',
        url=SEARCH + '?' + urllib.parse.urlencode({'jql': jql}),
        headers={
            'Content-Type': 'application/json',
            'Cookie': f'JSESSIONID={session}'}))


def partition(l, pred):
    ret = [], []
    for x in l:
        ret[pred(x)].append(x)
    return ret


def print_issues(unf, fin):
    fmt = f'<a href="{BROWSE}/{{key}}">{{key}}</a> {{fields[summary]}}<br />'
    key = lambda x: x['key']
    print('Finished<br />')
    for x in sorted(fin, key=key):
        print(fmt.format(**x))
    print('Unfinished<br />')
    for x in sorted(unf, key=key):
        print(fmt.format(**x))


if __name__ == '__main__':
    main()
