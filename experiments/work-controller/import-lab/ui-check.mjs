import {writeFile} from 'node:fs/promises';
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const browser=await chromium.launch(process.env.CHROMIUM_PATH ? {executablePath:process.env.CHROMIUM_PATH} : {});
const rows=[];
try {
 for (const [arm,port] of [['controller',4341],['native',4342]]) {
  const page=await browser.newPage({viewport:{width:390,height:844}});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`http://127.0.0.1:${port}`);
  await page.locator('input[type=file]').setInputFiles({name:'sample.csv',mimeType:'text/csv',buffer:Buffer.from('id,name,email\n1,Ada,ada@example.com\n2,Bad,bad\n')});
  await page.waitForFunction(()=>document.querySelector('#csv').value.includes('2,Bad,bad'));
  let responseSeen=false;
  const waiting=page.waitForResponse(r=>r.url().endsWith('/imports') && r.request().method()==='POST',{timeout:3000}).then(()=>{responseSeen=true;}).catch(()=>{});
  await page.click('#submit');await waiting;
  if(responseSeen) await page.waitForFunction(()=>/Row 3/.test(document.body.innerText),null,{timeout:3000}).catch(()=>{});
  await page.screenshot({path:`/tmp/import-${arm}-ui.png`,fullPage:true});
  const body=await page.locator('body').innerText();
  rows.push({arm,responseSeen,errors,overflow:await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),showsRowError:/Row 3/.test(body),body});
  await page.close();
 }
} finally {await browser.close();}
await writeFile(process.argv[2] || '/tmp/import-ui.json',JSON.stringify(rows,null,2)+'\n');
console.log(JSON.stringify(rows.map(({body,...row})=>row),null,2));
if(rows.some(r=>r.errors.length||!r.responseSeen||!r.showsRowError||r.overflow))process.exitCode=1;
