"""Real disposable Git evidence for every reviewed proof class; never scans secrets."""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

spec=importlib.util.spec_from_file_location('history',Path(__file__).with_name('security-history-policy.py'))
p=importlib.util.module_from_spec(spec);spec.loader.exec_module(p)
CONFIG=Path('/root/go/pkg/mod/github.com/zricethezav/gitleaks/v8@v8.30.1/config/gitleaks.toml')

class GitProofTest(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup);self.root=Path(self.temp.name)
        def git(*args):
            return subprocess.run(['git',*args],cwd=self.root,check=True,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,timeout=5).stdout.decode().strip()
        self.git=git
        git('init','-q');git('config','user.name','Synthetic fixture');git('config','user.email','fixture@example.invalid')
        self.source=b'package fixture\n';(self.root/'auth.go').write_bytes(self.source)
        self.producer=b'package fixture\n// uuid.New() supplies only synthetic fixture IDs\n// --target-key=api is a fixed CLI test name\n'
        (self.root/'producer.go').write_bytes(self.producer)
        git('add','.');git('commit','-qm','synthetic producer');self.first=git('rev-parse','HEAD')
        self.uuid='11111111-2222-4333-8444-555555555555'
        checksum='h1:'+'A'*43+'='
        self.values={
          'digest.json':p.canonical({'source_inputs':{'auth.go':p.sha(self.source)},'other_token':'synthetic_unreviewed_credential'}),
          'buildinfo.log':('dep cloud.google.com/go/auth v0.23.2 '+checksum+'\n').encode(),
          'go.sum':('cloud.google.com/go/auth v0.23.2 '+checksum+'\n').encode(),
          'commit.json':p.canonical({'sourceCommit':self.first,'other_token':'synthetic_unreviewed_credential'}),
          'metadata.json':p.canonical({'binaries':[{'buildMetadata':'build vcs.revision='+self.first}]}),
          'repo.json':p.canonical({'Test':'TestSyntheticRepository','Output':'auth repository_id='+self.uuid+' other_token=synthetic_unreviewed_credential\n'}),
          'task.log':('auth task_id='+self.uuid+' other_token=synthetic_unreviewed_credential\n').encode(),
          'backend.json':p.canonical({'Test':'TestSyntheticBackend','Output':'auth backend_id='+self.uuid+'\n'}),
          'test.json':p.canonical({'Test':'TestCommands/--target-key=api_--limit=7_--offset=2'}),
          'version.py':b'def test_runtime_variants_do_not_accept_prefix_or_arbitrary_suffix():\n    for name, bad_tag in [("api", "v9.1.2-invalid")]:\n        assert bad_tag != "v9.1.2"\n',
        }
        for name,value in self.values.items():(self.root/name).write_bytes(value)
        git('add','.');git('commit','-qm','synthetic evidence');self.commit=git('rev-parse','HEAD')
        self.evidence=p.GitEvidence(self.root,CONFIG)
        producer=[dict(path='producer.go',commit=self.first,sha256=p.sha(self.producer),lines=[2],static_statement='uuid.New()')]
        self.cases=[
          ('verified-public-file-digest','digest.json',dict(field='/source_inputs/auth.go',referenced_path='auth.go',referenced_commit=self.first,referenced_blob_sha256=p.sha(self.source),field_components=['source_inputs','auth.go'],field_rfc6901='/source_inputs/auth.go'),p.sha(self.source)),
          ('public-locked-module-checksum','buildinfo.log',dict(module='cloud.google.com/go/auth',version='v0.23.2',field='go-buildinfo.dep.sum',go_sum_path='go.sum',go_sum_sha256=p.sha(self.values['go.sum'])),checksum[3:]),
          ('public-git-commit','commit.json',dict(field='/sourceCommit',git_commit_object_exists=True,source_has_sgp_token=False),self.first),
          ('public-build-vcsrevision','metadata.json',dict(field='/binaries/0/buildMetadata',git_commit_object_exists=True,no_sgp_token=True),self.first),
          ('repository-uuid-test-log','repo.json',dict(field='Output.repository_id',go_test_event=True,test='TestSyntheticRepository',producer_sources=producer),self.uuid),
          ('task-uuid-test-log','task.log',dict(field='Output.task_id',go_test_event=False,test='',producer_sources=producer),self.uuid),
          ('backend-uuid-test-log','backend.json',dict(field='Output.backend_id',go_test_event=True,test='TestSyntheticBackend',producer_sources=producer),self.uuid),
          ('literal-test-case-name','test.json',dict(test='TestCommands/--target-key=api_--limit=7_--offset=2',producer_sources=[dict(path='producer.go',commit=self.first,sha256=p.sha(self.producer),lines=[3],static_statement='--target-key=api')]),'api_--limit=7_--offset=2'),
          ('fixed-runtime-version-negative-fixture','version.py',dict(function='test_runtime_variants_do_not_accept_prefix_or_arbitrary_suffix',field='literal iteration table name/bad_tag',source_sha256=p.sha(self.values['version.py'])),'v9.1.2-invalid'),
        ]

    def control(self,case,target=None):
        kind,path,proof,wanted=case;rule='sourcegraph-access-token' if kind.startswith('public-git') or kind=='public-build-vcsrevision' else 'generic-api-key'
        blob=self.evidence.blob(self.commit,path);found=[]
        for index,line in enumerate(blob.decode().splitlines()):
            found += [(index+1,line,m) for m in self.evidence.patterns[rule].finditer(line) if m[1]==(wanted if target is None else target)]
        self.assertEqual(len(found),1);number,line,m=found[0]
        site=dict(Commit=self.commit,File=path,RuleID=rule,Fingerprint='synthetic-'+kind,StartLine=number,EndLine=number,StartColumn=m.start()+1,EndColumn=m.end())
        column=dict(actual_blob_columns=[m.start()+1,m.end()],span_sha256=p.sha(m[0].encode()),reviewed_origin='disposable actual Git fixture, no release authority',replay_row_sha256='0'*64)
        return dict(index=0,site=site,blob_sha256=p.sha(blob),classification=kind,proof=copy.deepcopy(proof),proof_sha256=p.sha(p.canonical(proof)),column_proof=column,column_proof_sha256=p.sha(p.canonical(column)))

    def test_all_nine_classes_use_actual_git_blobs(self):
        self.assertEqual({case[0] for case in self.cases},p.CLASSES)
        for case in self.cases:
            with self.subTest(kind=case[0]):p.verify_proof(self.control(case),self.evidence)

    def test_same_line_other_token_cannot_borrow_public_digest_or_uuid_proof(self):
        for index in [0,4,5]:
            with self.subTest(kind=self.cases[index][0]):
                row=self.control(self.cases[index],'synthetic_unreviewed_credential')
                with self.assertRaises(ValueError):p.verify_proof(row,self.evidence)

    def test_same_uuid_in_other_output_field_does_not_borrow_label(self):
        blob=p.canonical({'Test':'TestSyntheticRepository','Output':'auth other_token='+self.uuid+' auth repository_id='+self.uuid+'\n'})
        (self.root/'repo.json').write_bytes(blob);self.git('add','.');self.git('commit','-qm','same UUID different field');self.commit=self.git('rev-parse','HEAD')
        line=blob.decode();match=next(m for m in self.evidence.patterns['generic-api-key'].finditer(line) if m[1]==self.uuid)
        # Build the exact earlier labelled proof but bind the other field's span.
        case=self.cases[4];row=dict(index=0,site=dict(Commit=self.commit,File='repo.json',RuleID='generic-api-key',Fingerprint='synthetic',StartLine=1,EndLine=1,StartColumn=match.start()+1,EndColumn=match.end()),blob_sha256=p.sha(blob),classification=case[0],proof=case[2],column_proof=dict(actual_blob_columns=[match.start()+1,match.end()],span_sha256=p.sha(match[0].encode()),reviewed_origin='synthetic',replay_row_sha256='0'*64))
        with self.assertRaises(ValueError):p.verify_proof(row,self.evidence)

    def test_fixed_test_name_output_requires_exact_go_test_event(self):
        case=self.cases[7]
        for prefix in ['=== RUN   ', '--- PASS: ']:
            output=prefix+case[2]['test']+(' (0.00s)\n' if prefix.startswith('---') else '\n')
            blob=p.canonical({'Action':'output','Test':case[2]['test'],'Output':output})
            (self.root/'test.json').write_bytes(blob);self.git('add','.');self.git('commit','-qm','owned Go test output');self.commit=self.git('rev-parse','HEAD')
            # Select the match in Output, after the earlier identical Test scalar.
            line=blob.decode();matches=[m for m in self.evidence.patterns['generic-api-key'].finditer(line) if m[1]==case[3]]
            output_start,output_end,_=p.scalar_span(blob,['Output']);match=next(m for m in matches if output_start<=m.start(1)<m.end(1)<=output_end);row=dict(site=dict(Commit=self.commit,File='test.json',RuleID='generic-api-key',Fingerprint='synthetic',StartLine=1,EndLine=1,StartColumn=match.start()+1,EndColumn=match.end()),blob_sha256=p.sha(blob),classification=case[0],proof=case[2],column_proof=dict(actual_blob_columns=[match.start()+1,match.end()],span_sha256=p.sha(match[0].encode()),reviewed_origin='synthetic',replay_row_sha256='0'*64))
            p.verify_proof(row,self.evidence)
        blob=p.canonical({'Action':'output','Test':case[2]['test'],'Output':'other_token='+case[3]+'\n'})
        (self.root/'test.json').write_bytes(blob);self.git('add','.');self.git('commit','-qm','unowned output');self.commit=self.git('rev-parse','HEAD')
        output_start,output_end,_=p.scalar_span(blob,['Output']);match=next(m for m in self.evidence.patterns['generic-api-key'].finditer(blob.decode()) if output_start<=m.start(1)<m.end(1)<=output_end)
        row.update(site=dict(row['site'],Commit=self.commit,StartColumn=match.start()+1,EndColumn=match.end()),blob_sha256=p.sha(blob),column_proof=dict(row['column_proof'],actual_blob_columns=[match.start()+1,match.end()],span_sha256=p.sha(match[0].encode())))
        with self.assertRaises(ValueError):p.verify_proof(row,self.evidence)

    def test_blob_size_rejected_before_blob_process(self):
        calls=[]
        def output(args,limit):
            calls.append(args);return str(p.MAX_BYTES+1).encode()+b'\n'
        with mock.patch.object(self.evidence,'output',side_effect=output),self.assertRaises(ValueError):
            self.evidence.blob(self.commit,'oversized.go')
        self.assertEqual(calls,[['cat-file','-s',self.commit+':oversized.go']])

    def test_actual_git_size_precheck_blocks_large_object(self):
        calls=[];original=self.evidence.output
        def observe(args,limit):
            calls.append(args);return original(args,limit)
        with mock.patch.object(self.evidence,'output',side_effect=observe),mock.patch.object(p,'MAX_BYTES',8),self.assertRaises(ValueError):
            self.evidence.blob(self.commit,'auth.go')
        self.assertEqual(calls,[['cat-file','-s',self.commit+':auth.go']])

    def test_git_read_timeout_is_propagated_without_stdout(self):
        with mock.patch('subprocess.run',side_effect=subprocess.TimeoutExpired('git',15)),self.assertRaises(subprocess.TimeoutExpired):
            self.evidence.blob(self.commit,'auth.go')

if __name__=='__main__':unittest.main()
