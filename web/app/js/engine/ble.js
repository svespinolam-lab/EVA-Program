/* EVA BLE adapter: standard GATT heart-rate, pulse-oximeter and thermometer profiles.
   Real hardware readings replace simulated values only for the paired metric. */
(function () {
  'use strict';
  var App=window.App=window.App||{}, links={};
  var SERVICES=['heart_rate','health_thermometer','pulse_oximeter'];
  function sfloat(dv,off) {
    var raw=dv.getUint16(off,true), mant=raw&0x0fff, exp=(raw>>12)&0x0f;
    if(mant>=0x0800)mant-=0x1000;if(exp>=0x08)exp-=0x10;
    return mant*Math.pow(10,exp);
  }
  function float11073(dv,off) {
    var raw=dv.getUint32(off,true), mant=raw&0x00ffffff, exp=(raw>>24)&0xff;
    if(mant&0x00800000)mant-=0x01000000;if(exp&0x80)exp-=0x100;
    return mant*Math.pow(10,exp);
  }
  function listen(device,serviceName,charName,crewId,key,decode) {
    return device.gatt.getPrimaryService(serviceName).then(function(s){return s.getCharacteristic(charName);})
      .then(function(ch){
        return ch.startNotifications().then(function(){
          ch.addEventListener('characteristicvaluechanged',function(ev){
            var v=decode(ev.target.value);
            if(Number.isFinite(v))App.telemetry.ingestHardware(crewId,key,v);
          });
          return ch;
        });
      });
  }
  function pair(crewId) {
    if(!navigator.bluetooth||!navigator.bluetooth.requestDevice)
      return Promise.reject(new Error('Bluetooth LE is unavailable here. Use Android Chrome over HTTPS or the native app.'));
    return navigator.bluetooth.requestDevice({
      filters:SERVICES.map(function(s){return {services:[s]};}),
      optionalServices:SERVICES
    }).then(function(device){
      return device.gatt.connect().then(function(){
        var tasks=[
          listen(device,'heart_rate','heart_rate_measurement',crewId,'hr',function(dv){var flags=dv.getUint8(0);return flags&1?dv.getUint16(1,true):dv.getUint8(1);}).catch(function(){return null;}),
          listen(device,'health_thermometer','temperature_measurement',crewId,'temp',function(dv){return float11073(dv,1);}).catch(function(){return null;}),
          listen(device,'pulse_oximeter','plx_continuous_measurement',crewId,'spo2',function(dv){return sfloat(dv,1);}).catch(function(){return null;})
        ];
        return Promise.all(tasks).then(function(chars){
          if(!chars.length)throw new Error('No supported measurement characteristic found on this device.');
          links[crewId]=links[crewId]||[];links[crewId].push({device:device,chars:chars});
          device.addEventListener('gattserverdisconnected',function(){
            ['hr','temp','spo2'].forEach(function(k){App.telemetry.clearHardware(crewId,k);});
            App.ui.toast('BLE device disconnected · values marked unavailable',{icon:'unlink',level:2});
          });
          App.ui.toast('BLE sensor paired · live readings enabled',{icon:'bluetooth'});
          return device.name||'BLE sensor';
        });
      });
    });
  }
  App.ble={pair:pair,connected:function(crewId){return !!(links[crewId]&&links[crewId].length);}};
})();