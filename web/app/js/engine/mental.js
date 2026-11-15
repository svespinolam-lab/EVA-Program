/* EVA · Private daily check-in. These are check-in rules, not a diagnosis.
 * Rule                                      | Section level | Overall level / flag
 * Section answer 0 (good) or 1 (middle)       | 0             | No flag
 * Section answer 2 (poor)                    | 1             | At least 1 / section flag
 * Three or more poor sections               | Unchanged     | 2
 * Sleep below 300 min (5 h)                  | N/A           | 2 / SHORT_SLEEP
 * Sleep 300 to below 420 min (7 h)           | N/A           | At least 1 / SHORT_SLEEP
 * Sleep at least 420 min; quality 0 or 1     | N/A           | No additional flag
 * Sleep quality 2 (Poor)                    | N/A           | At least 1 / POOR_SLEEP
 * Missing/invalid answer or sleep           | null / 0      | Ignored; summary says incomplete
 * Personal safety (0 Yes, 1 Not sure, 2 No) | Not evaluated | Phone support only; no score/flag
 * Overall = maximum applicable level, always 0..2. Flags follow section order, then sleep.
 *
 * Legacy mood: 0 Low, 1 Steady, 2 Good; energy: 0 Low, 1 Medium, 2 High.
 * New answers run good to poor: new = 2 - legacy, legacy = 2 - new.
 * fromLegacy() copies known fields, prefers valid nested answers, and fills only
 * missing mood/energy from legacy fields. Other missing answers stay null.
 * toLegacy() is a whitelist projection: mood, energy, sleepMin, at, example.
 * It NEVER copies safety, answers, or quality. Safety cannot affect either
 * evaluation or this legacy projection (including summaries and flags).
 */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  function options(labels) {
    return labels.map(function (label, value) { return { label: label, value: value }; });
  }
  function section(id, title, question, labels, flag) {
    return { id: id, title: title, question: question, options: options(labels), flag: flag };
  }
  var SECTIONS = [
    section('mood', 'Mood', 'How are you feeling today?', ['Good', 'Steady', 'Low'], 'LOW_MOOD'),
    section('energy', 'Energy and fatigue', 'How much energy do you have today?', ['High', 'Medium', 'Low'], 'FATIGUE'),
    section('stress', 'Stress and anxiety', 'How manageable do worries feel today?', ['Manageable', 'Some strain', 'Overwhelming'], 'STRESS'),
    section('interest', 'Interest and drive', 'How interested do you feel in your usual activities?', ['Interested', 'Less interested', 'Little interest'], 'APATHY'),
    section('attention', 'Attention', 'How easy is it to focus today?', ['Easy', 'Some effort', 'Difficult'], 'ATTENTION'),
    section('team', 'Team and connection', 'How connected do you feel to your crew?', ['Connected', 'Some distance', 'Disconnected'], 'ISOLATION'),
    section('lonely', 'Loneliness and homesickness', 'How much are loneliness or missing home weighing on you?', ['Very little', 'Somewhat', 'A lot'], 'LONELINESS'),
    section('morale', 'Motivation and morale', 'How motivated do you feel for the day ahead?', ['Motivated', 'Somewhat motivated', 'Little motivation'], 'LOW_MORALE')
  ];
  var sleepConfig = App.config && App.config.mental && App.config.mental.sleep;
  var SLEEP = {
    title: 'Sleep', question: 'How did you sleep?',
    hours: { field: 'sleepMin', stepMin: 15, recommendedMin: 420,
      defaultMin: sleepConfig ? sleepConfig.defaultMin : 435,
      maxMin: sleepConfig ? sleepConfig.maxMin : 720 },
    quality: { field: 'sleepQuality', question: 'How restful was your sleep?',
      options: options(['Restful', 'Fair', 'Poor']) }
  };
  var SAFETY = {
    title: 'Personal safety',
    question: 'Right now, do you feel safe and able to carry on with your duties?',
    options: options(['Yes', 'Not sure', 'No'])
  };
  function answer(v) { return v === 0 || v === 1 || v === 2 ? v : null; }
  function minutes(v) { return typeof v === 'number' && isFinite(v) && v >= 0 ? v : null; }
  function fromLegacy(checkin) {
    var c = checkin || {}, answers = {}, source = c.answers || {};
    SECTIONS.forEach(function (s) {
      var v = answer(source[s.id]);
      if (v === null && (s.id === 'mood' || s.id === 'energy') && answer(c[s.id]) !== null) v = 2 - c[s.id];
      answers[s.id] = v;
    });
    return { answers: answers, sleepMin: minutes(c.sleepMin), sleepQuality: answer(c.sleepQuality),
      safety: answer(c.safety), at: c.at, example: c.example === true };
  }
  function toLegacy(checkin) {
    var c = fromLegacy(checkin);
    return { mood: c.answers.mood === null ? null : 2 - c.answers.mood,
      energy: c.answers.energy === null ? null : 2 - c.answers.energy,
      sleepMin: c.sleepMin, at: c.at, example: c.example };
  }
  function evaluate(checkin) {
    var c = fromLegacy(checkin), flags = [], poor = 0, level = 0, missing = false;
    var sections = SECTIONS.map(function (s) {
      var v = c.answers[s.id];
      if (v === null) missing = true;
      if (v === 2) { poor++; flags.push(s.flag); }
      return { id: s.id, value: v, level: v === 2 ? 1 : 0 };
    });
    level = poor >= 3 ? 2 : poor ? 1 : 0;
    if (c.sleepMin === null || c.sleepQuality === null) missing = true;
    if (c.sleepMin !== null && c.sleepMin < SLEEP.hours.recommendedMin) {
      flags.push('SHORT_SLEEP');
      level = Math.max(level, c.sleepMin < 300 ? 2 : 1);
    }
    if (c.sleepQuality === 2) { flags.push('POOR_SLEEP'); level = Math.max(level, 1); }
    var summary = level === 2 ? 'Several concerns or very short sleep; talk with the crew medical officer.' :
      level === 1 ? 'Some concerns flagged; allow time for rest and support.' : 'No concerns flagged in answered items.';
    if (missing) summary += ' Check-in incomplete.';
    return { sections: sections, level: level, flags: flags, summary: summary };
  }
  App.mental = { SECTIONS: SECTIONS, SLEEP: SLEEP, SAFETY: SAFETY,
    evaluate: evaluate, toLegacy: toLegacy, fromLegacy: fromLegacy };
})();
